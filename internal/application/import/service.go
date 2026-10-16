package importer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	auth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	Store app.Store
	Auth  *auth.Service
	Bytes Bytes
	Codec Codec
	Now   func() time.Time
}

func New(store app.Store, a *auth.Service, b Bytes, c Codec) *Service {
	return &Service{Store: store, Auth: a, Bytes: b, Codec: c, Now: time.Now}
}
func (s *Service) now() string { return s.Now().UTC().Format(time.RFC3339Nano) }

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func encoded(v any) []byte { b, _ := json.Marshal(v); return b }
func strptr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func (s *Service) request(ctx context.Context, token, key, method, target string, body any) (domain.Request, error) {
	a, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Request{}, e
	}
	if !idPattern.MatchString(key) {
		return domain.Request{}, sec.ErrInvalid
	}
	return domain.Request{Key: domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: key}, Method: method, Target: target, Body: encoded(body)}, nil
}
func (s *Service) current(ctx context.Context, tx app.WriteTx, token string, u domain.Upload) error {
	r := domain.Request{Key: domain.OperationKey{PrincipalID: u.PrincipalID}, Method: "GET", Target: "/api/v1/uploads/" + u.ID}
	if e := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, r); e != nil {
		return e
	}
	return targetAuthority(tx, u.Purpose, u.Target)
}
func targetAuthority(tx app.WriteTx, purpose string, target []byte) error {
	normalized, e := app.NormalizeTarget(purpose, target)
	if e != nil {
		return sec.ErrInvalid
	}
	return app.AuthorizeTarget(tx, normalized)
}

func (s *Service) Create(ctx context.Context, token, key string, input CreateRequest) (domain.Response, error) {
	r, e := s.request(ctx, token, key, "POST", "/api/v1/uploads", input)
	if e != nil {
		return domain.Response{}, e
	}
	normalized, e := app.NormalizeTarget(input.Purpose, input.Target)
	if e != nil {
		return domain.Response{}, sec.ErrInvalid
	}
	input.Target = normalized
	r.Body = encoded(input)
	device, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, func() ([]byte, error) {
		id, e := service.NewID()
		return encoded(map[string]string{"upload_id": id, "created_at": s.now()}), e
	}, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		if e := targetAuthority(tx, input.Purpose, input.Target); e != nil {
			return domain.Response{}, e
		}
		var n map[string]string
		_ = json.Unmarshal(receipt.Normalization, &n)
		u := domain.Upload{ID: n["upload_id"], PrincipalID: r.Key.PrincipalID, DeviceID: device.ID, Purpose: input.Purpose, InputMode: input.InputMode, SelectionName: input.SelectionName, Target: input.Target, CreatedAt: n["created_at"], UpdatedAt: n["created_at"], Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: app.EmptyInventorySHA256, AcceptedBytes: "0"}}
		if e := tx.CreateUpload(u); e != nil {
			return domain.Response{}, e
		}
		return domain.Response{Status: 201, Body: encoded(map[string]any{"upload_id": u.ID, "state": "open", "checkpoint": u.Checkpoint, "transport_entry": transport(u)})}, nil
	})
}
func transport(u domain.Upload) *string {
	if u.InputMode == "directory" {
		return nil
	}
	v := "_input"
	return &v
}
func parents(path string) []string {
	parts := strings.Split(path, "/")
	out := []string{}
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}
func (s *Service) Put(ctx context.Context, token, id string, c Chunk, input io.Reader) (ChunkResult, error) {
	if _, e := s.Auth.Authenticate(ctx, token); e != nil {
		return ChunkResult{}, e
	}
	if e := ValidatePath(c.Path); e != nil {
		return ChunkResult{}, e
	}
	if c.Kind != "file" && c.Kind != "directory" && c.Kind != "symlink" {
		return ChunkResult{}, sec.ErrInvalid
	}
	if c.Offset < 0 || (c.Length != nil && *c.Length < 0) {
		return ChunkResult{}, sec.ErrInvalid
	}
	var out ChunkResult
	commit := func(tx app.WriteTx, object domain.Object) error {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return e
		}
		if u.State != "open" || u.Checkpoint.Complete {
			return domain.ErrTransition
		}
		if u.InputMode != "directory" && (c.Kind != "file" || c.Path != "_input") {
			return sec.ErrInvalid
		}
		for _, p := range parents(c.Path) {
			if e := tx.PutUploadEntry(domain.UploadEntry{UploadID: id, Path: p, Kind: "directory", Final: true}); e != nil {
				return ErrInputChanged
			}
		}
		length := c.Length
		if c.Kind == "file" {
			if object.Hash != c.Hash {
				return ErrHashMismatch
			}
			if object.Size > math.MaxInt64-c.Offset {
				return sec.ErrInvalid
			}
			end := c.Offset + object.Size
			if length != nil && end > *length {
				return ErrInputChanged
			}
			if c.Final {
				if length != nil && *length != end {
					return ErrInputChanged
				}
				length = &end
			}
		}
		entry := domain.UploadEntry{UploadID: id, Path: c.Path, Kind: c.Kind, LinkText: c.LinkTarget, ExpectedSize: length, Final: c.Final || c.Kind != "file"}
		if e := tx.PutUploadEntry(entry); e != nil {
			if errors.Is(e, domain.ErrConflict) {
				return ErrInputChanged
			}
			return e
		}
		if c.Kind == "file" {
			h, e := tx.TransferDigest(id)
			if e != nil {
				return e
			}
			u, e = tx.Upload(id)
			if e != nil {
				return e
			}
			_, e = tx.RecordRange(domain.UploadRange{UploadID: id, Path: c.Path, Offset: c.Offset, Size: object.Size, Hash: object.Hash}, u.Checkpoint.Generation, h)
			if e != nil {
				if errors.Is(e, domain.ErrConflict) {
					return ErrInputChanged
				}
				return e
			}
			if e := tx.ReleasePin("chunk-temp:" + id + ":" + c.Path + ":" + strconv.FormatInt(c.Offset, 10)); e != nil {
				return e
			}
		}
		if e := tx.TouchUpload(id, s.now()); e != nil {
			return e
		}
		fresh, e := tx.Upload(id)
		out.Checkpoint = fresh.Checkpoint
		out.Offset = strconv.FormatInt(c.Offset+object.Size, 10)
		if e != nil {
			return e
		}
		return s.current(ctx, tx, token, fresh)
	}
	if c.Kind == "file" {
		if !hashPattern.MatchString(c.Hash) {
			return out, sec.ErrInvalid
		}
		_, e := s.Bytes.Put(ctx, input, commit)
		return out, e
	}
	if c.Kind == "symlink" {
		if _, e := InventoryLine(domain.Entry{Path: c.Path, Kind: c.Kind, LinkText: c.LinkTarget}); e != nil {
			return out, e
		}
	}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { return commit(tx, domain.Object{}) })
	return out, e
}
func (s *Service) Complete(ctx context.Context, token, id, key string, cp domain.Checkpoint) (domain.Response, error) {
	r, e := s.request(ctx, token, key, "POST", "/api/v1/uploads/"+id+"/complete", map[string]any{"checkpoint": cp})
	if e != nil {
		return domain.Response{}, e
	}
	if e := app.ValidateCheckpoint(cp); e != nil || !cp.Complete {
		return domain.Response{}, sec.ErrInvalid
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return domain.Response{}, e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return domain.Response{}, e
		}
		if !u.Checkpoint.Complete {
			if u.Checkpoint.Generation == math.MaxInt64 || cp.Generation != u.Checkpoint.Generation+1 || cp.InventorySHA256 != u.Checkpoint.InventorySHA256 || cp.AcceptedBytes != u.Checkpoint.AcceptedBytes {
				return domain.Response{}, domain.ErrTransition
			}
			if e := tx.UploadComplete(id); e != nil {
				return domain.Response{}, e
			}
			if _, e := tx.CompleteCheckpoint(id, u.Checkpoint.Generation, cp.InventorySHA256); e != nil {
				return domain.Response{}, e
			}
		}
		job, e := service.NewID()
		if e != nil {
			return domain.Response{}, e
		}
		response, e := tx.CompleteUpload(id, cp, job)
		if e != nil {
			return response, e
		}
		if e := tx.SetSealOperation(id, key); e != nil {
			return response, e
		}
		if response.Status == 202 {
			u, e = tx.Upload(id)
			if e != nil {
				return response, e
			}
			j, e := tx.Job(u.SealJobID)
			if e != nil {
				return response, e
			}
			if len(j.Scope) == 2 {
				l, e := tx.Library()
				if e != nil {
					return response, e
				}
				scope := encoded(map[string]any{"principal_id": u.PrincipalID, "auth_epoch": l.AuthEpoch, "purpose": u.Purpose, "target": json.RawMessage(u.Target)})
				j.Scope = scope
				var target map[string]any
				_ = json.Unmarshal(u.Target, &target)
				project, _ := target["project_id"].(string)
				current, e := tx.Scope(project)
				if e != nil {
					return response, e
				}
				j.ConfigRevision = current.ConfigRevision
				j.Permissions = map[string]int64{}
				if project != "" {
					j.Permissions[project] = current.Project.PermissionsRevision
				}
				if e := tx.ConfigureJobScope(j); e != nil {
					return response, e
				}
			}
		}
		return response, nil
	})
}
func (s *Service) Abandon(ctx context.Context, token, id, key string) (domain.Response, error) {
	r, e := s.request(ctx, token, key, "POST", "/api/v1/uploads/"+id+"/abandon", struct{}{})
	if e != nil {
		return domain.Response{}, e
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return domain.Response{}, e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return domain.Response{}, e
		}
		if e := tx.AbandonUpload(id); e != nil {
			return domain.Response{}, e
		}
		if e := tx.ReclaimAbandonedUpload(id); e != nil {
			return domain.Response{}, e
		}
		return domain.Response{Status: 200, Body: encoded(map[string]string{"upload_id": id, "state": "abandoned"})}, nil
	})
}
func (s *Service) Upload(ctx context.Context, token, id string) (UploadDetail, error) {
	var out UploadDetail
	if _, e := s.Auth.Authenticate(ctx, token); e != nil {
		return out, e
	}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return e
		}
		out = detail(u)
		stats, e := tx.UploadStats(u.ID)
		if e != nil {
			return e
		}
		decorateStats(&out.UploadView, stats)
		return nil
	})
	if e == nil && out.State == "sealed" {
		available, reason, err := s.available(ctx, token, id)
		if err != nil && reason == nil {
			return out, err
		}
		out.Reusable = available
		out.UnavailableReason = reason
	}
	if e == nil {
		decorateUploadETag(&out.UploadView)
	}
	return out, e
}
func summary(u domain.Upload) UploadView {
	b := u.Checkpoint.AcceptedBytes
	return UploadView{ID: u.ID, PrincipalID: u.PrincipalID, DeviceID: strptr(u.DeviceID), Purpose: u.Purpose, InputMode: u.InputMode, SelectionName: strptr(u.SelectionName), Target: u.Target, State: u.State, ByteSize: b, StagingBytes: b, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, Reusable: u.State == "sealed", PinSummary: map[string]int{"staging": 0, "adopted": 0, "active_work": 0, "recovery": 0}, PinsURL: "/api/v1/uploads/" + u.ID + "/pins", DetailURL: "/api/v1/uploads/" + u.ID, Revision: u.Revision, ETag: fmt.Sprintf("\"upload-%s-r%d\"", u.ID, u.Revision)}
}
func detail(u domain.Upload) UploadDetail {
	d := UploadDetail{UploadView: summary(u), Checkpoint: u.Checkpoint, RangesURL: "/api/v1/uploads/" + u.ID + "/ranges", EntriesURL: "/api/v1/uploads/" + u.ID + "/entries", Error: strptr(u.Error), SealJobID: strptr(u.SealJobID), SealOperationID: strptr(u.SealOperationID), ContentSetID: strptr(u.ContentSetID), TransportEntry: transport(u)}
	if u.ObjectHash != "" {
		d.ObjectRef = &ObjectRef{ID: u.ID, Hash: u.ObjectHash, Size: u.Checkpoint.AcceptedBytes, MediaType: "application/octet-stream"}
	}
	return d
}
func (s *Service) Uploads(ctx context.Context, token string, q UploadQuery) (Page[UploadView], error) {
	out := Page[UploadView]{Items: []UploadView{}}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		rows, e := tx.Uploads(q.Cursor, q.State, q.Unadopted, 64)
		if e != nil {
			return e
		}
		for _, u := range rows {
			if record.PrincipalID != u.PrincipalID {
				continue
			}
			if e := targetAuthority(tx, u.Purpose, u.Target); e != nil {
				if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrInvalidTarget) {
					continue
				}
				return e
			}
			view := summary(u)
			stats, e := tx.UploadStats(u.ID)
			if e != nil {
				return e
			}
			decorateStats(&view, stats)
			out.Items = append(out.Items, view)
		}
		if len(rows) == 64 {
			out.NextCursor = strptr(rows[len(rows)-1].ID)
		}
		return nil
	})
	if e == nil {
		for n := range out.Items {
			if out.Items[n].State == "sealed" {
				available, reason, err := s.available(ctx, token, out.Items[n].ID)
				if err != nil && reason == nil {
					return out, err
				}
				out.Items[n].Reusable = available
				out.Items[n].UnavailableReason = reason
			}
		}
	}
	if e == nil {
		for n := range out.Items {
			decorateUploadETag(&out.Items[n])
		}
	}
	return out, e
}
func (s *Service) Entries(ctx context.Context, token, id, cursor string) (Page[EntryProgress], error) {
	out := Page[EntryProgress]{Items: []EntryProgress{}}
	if _, e := s.Auth.Authenticate(ctx, token); e != nil {
		return out, e
	}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return e
		}
		rows, e := tx.UploadEntries(id, cursor, 64)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Items = append(out.Items, EntryProgress{Path: r.Path, Kind: r.Kind, LinkTarget: strptr(r.LinkText), ByteSize: strconv.FormatInt(r.Size, 10), AcceptedBytes: strconv.FormatInt(r.Size, 10), Complete: r.Final})
		}
		if len(rows) == 64 {
			out.NextCursor = strptr(rows[len(rows)-1].Path)
		}
		return nil
	})
	return out, e
}
func (s *Service) Ranges(ctx context.Context, token, id, cursor, path string) (Page[domain.UploadRange], error) {
	out := Page[domain.UploadRange]{Items: []domain.UploadRange{}}
	if _, e := s.Auth.Authenticate(ctx, token); e != nil {
		return out, e
	}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return e
		}
		var c struct {
			Path   string
			Offset int64
		}
		c.Offset = -1
		if cursor != "" {
			if json.Unmarshal([]byte(cursor), &c) != nil {
				return sec.ErrInvalid
			}
		}
		rows, e := tx.Ranges(id, c.Path, c.Offset, 64)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if path == "" || r.Path == path {
				out.Items = append(out.Items, r)
			}
		}
		if len(rows) == 64 {
			last := rows[len(rows)-1]
			v := string(encoded(map[string]any{"Path": last.Path, "Offset": last.Offset}))
			out.NextCursor = &v
		}
		return nil
	})
	return out, e
}
func (s *Service) Pins(ctx context.Context, token, id, cursor string) (Page[domain.Pin], error) {
	out := Page[domain.Pin]{Items: []domain.Pin{}}
	if _, e := s.Auth.Authenticate(ctx, token); e != nil {
		return out, e
	}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		u, e := s.authorizedUpload(ctx, tx, token, id)
		if e != nil {
			return e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return e
		}
		out.Items, e = tx.UploadPins(id, cursor, 64)
		if len(out.Items) == 64 {
			out.NextCursor = strptr(out.Items[len(out.Items)-1].ID)
		}
		return e
	})
	return out, e
}

func (s *Service) authorizedUpload(ctx context.Context, tx app.WriteTx, token, id string) (domain.Upload, error) {
	if _, e := s.Auth.AuthenticateWrite(ctx, tx, token); e != nil {
		return domain.Upload{}, e
	}
	u, e := tx.Upload(id)
	if e != nil {
		return u, e
	}
	return u, s.current(ctx, tx, token, u)
}

func decorateStats(view *UploadView, s domain.UploadStats) {
	view.StagingBytes = s.StagingBytes
	view.PinSummary = map[string]int{"staging": s.Staging, "adopted": s.Adopted, "active_work": s.Active, "recovery": s.Recovery}
}
func (s *Service) available(ctx context.Context, token, id string) (bool, *string, error) {
	after := ""
	for {
		var rows []domain.Entry
		e := s.Store.Write(ctx, func(tx app.WriteTx) error {
			u, e := s.authorizedUpload(ctx, tx, token, id)
			if e != nil {
				return e
			}
			if u.State != "sealed" {
				return domain.ErrTransition
			}
			rows, e = tx.Entries(u.ContentSetID, after, 64)
			return e
		})
		if e != nil {
			return false, nil, e
		}
		if len(rows) == 0 {
			return true, nil, nil
		}
		for _, entry := range rows {
			after = entry.Path
			if entry.Kind != "file" {
				continue
			}
			e := s.Auth.Guard(ctx, token, func() error {
				return s.Bytes.Read(ctx, domain.Object{Hash: entry.Hash, Size: entry.Size}, domain.Pin{Kind: "upload_read", OwnerID: id}, func(io.ReadSeeker) error { return nil })
			})
			if e != nil {
				if errors.Is(e, sec.ErrRequired) || errors.Is(e, sec.ErrExpired) || errors.Is(e, sec.ErrRevoked) || errors.Is(e, domain.ErrDenied) || ctx.Err() != nil {
					return false, nil, e
				}
				reason := "retained bytes unavailable or unverifiable"
				return false, &reason, e
			}
		}
	}
}

func decorateUploadETag(v *UploadView) {
	v.ETag = ""
	hash := sha256.Sum256(encoded(v))
	v.ETag = fmt.Sprintf("\"upload-%s-%x\"", v.ID, hash)
}
