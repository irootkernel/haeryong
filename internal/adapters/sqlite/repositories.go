package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type rowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func receipt(ctx context.Context, q rowReader, key domain.OperationKey) (domain.Receipt, error) {
	r := domain.Receipt{Key: key}
	var original, result []byte
	err := q.QueryRowContext(ctx, "SELECT request_fingerprint,original_request,normalization_context,state,result FROM operation_receipts WHERE principal_id=? AND operation_id=?", key.PrincipalID, key.OperationID).Scan(&r.Fingerprint, &original, &r.Normalization, &r.State, &result)
	if err != nil {
		return r, notFound(err)
	}
	if err := json.Unmarshal(original, &r.Original); err != nil {
		return r, err
	}
	if r.State == "completed" || r.State == "failed" {
		r.Response = &domain.Response{}
		if err := json.Unmarshal(result, r.Response); err != nil {
			return r, err
		}
	}
	return r, nil
}
func (s *Store) Receipt(ctx context.Context, key domain.OperationKey) (domain.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return receipt(ctx, s.db, key)
}
func (t *writeTx) ReserveOperation(request domain.Request, normalize func() ([]byte, error)) (domain.Receipt, bool, error) {
	r, err := receipt(t.ctx, t.tx, request.Key)
	if err == nil {
		if purged, err := app.CheckPurgedReceipt(request, r); purged || err != nil {
			return r, true, err
		}
		fingerprint, err := app.Fingerprint(request, r.Normalization)
		if err != nil {
			return r, true, err
		}
		if fingerprint != r.Fingerprint {
			return r, true, domain.ErrConflict
		}
		return r, true, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return r, false, err
	}
	normalization := []byte(`{}`)
	if normalize != nil {
		normalization, err = normalize()
		if err != nil {
			return r, false, err
		}
		if normalization == nil {
			normalization = []byte(`{}`)
		}
	}
	fingerprint, err := app.Fingerprint(request, normalization)
	if err != nil {
		return r, false, err
	}
	r = domain.Receipt{Key: request.Key, Fingerprint: fingerprint, Original: request, Normalization: normalization, State: "accepted"}
	err = t.exec("INSERT INTO operation_receipts VALUES(?,?,?,?,?,'accepted',NULL,NULL)", request.Key.PrincipalID, request.Key.OperationID, fingerprint, jsonBytes(request), normalization)
	return r, false, err
}
func (t *writeTx) CompleteOperation(key domain.OperationKey, response domain.Response) error {
	if response.Status < 100 || response.Status > 599 {
		return errors.New("invalid recorded HTTP status")
	}
	r, err := receipt(t.ctx, t.tx, key)
	if err != nil {
		return err
	}
	if r.State == "completed" || r.State == "failed" {
		if string(jsonBytes(r.Response)) == string(jsonBytes(response)) {
			return nil
		}
		return domain.ErrConflict
	}
	return t.exec("UPDATE operation_receipts SET state=?,original_status=?,result=? WHERE principal_id=? AND operation_id=?", receiptState(response.Status), response.Status, jsonBytes(response), key.PrincipalID, key.OperationID)
}
func resource(ctx context.Context, q rowReader, kind, id string) (domain.Resource, error) {
	r := domain.Resource{Kind: kind, ID: id}
	err := q.QueryRowContext(ctx, "SELECT revision,value FROM resource_revisions WHERE resource_kind=? AND resource_id=?", kind, id).Scan(&r.Revision, &r.Value)
	return r, notFound(err)
}
func (s *Store) Resource(ctx context.Context, kind, id string) (domain.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return resource(ctx, s.db, kind, id)
}
func (t *writeTx) MutateResource(r domain.Resource, expected int64, kind string) (domain.Resource, error) {
	if r.Kind == "" || r.ID == "" || kind == "" {
		return r, errors.New("resource/event identity required")
	}
	old, err := resource(t.ctx, t.tx, r.Kind, r.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return r, err
	}
	if (errors.Is(err, domain.ErrNotFound) && expected != 0) || (err == nil && old.Revision != expected) {
		return r, domain.ErrPrecondition
	}
	r.Revision = expected + 1
	if err := t.exec("INSERT INTO resource_revisions VALUES(?,?,?,?) ON CONFLICT(resource_kind,resource_id) DO UPDATE SET revision=excluded.revision,value=excluded.value", r.Kind, r.ID, r.Revision, r.Value); err != nil {
		return r, err
	}
	return r, t.appendEvent(r.Kind, r.ID, kind, r.Revision, nil)
}
func (t *writeTx) appendEvent(resourceKind, id, kind string, revision int64, body []byte) error {
	return t.exec("INSERT INTO events(stream_epoch,resource_kind,resource_id,kind,revision,body) SELECT stream_epoch,?,?,?,?,? FROM libraries WHERE singleton=1", resourceKind, id, kind, revision, body)
}
func batch(limit int) int {
	if limit < 1 || limit > 256 {
		return 256
	}
	return limit
}
func (s *Store) Events(ctx context.Context, epoch string, after int64, limit int) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current string
	if err := s.db.QueryRowContext(ctx, "SELECT stream_epoch FROM libraries").Scan(&current); err != nil {
		return nil, err
	}
	if epoch != current {
		return nil, domain.ErrPrecondition
	}
	rows, err := s.db.QueryContext(ctx, "SELECT stream_epoch,sequence,resource_kind,resource_id,kind,revision,body FROM events WHERE sequence>? ORDER BY sequence LIMIT ?", after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.StreamEpoch, &e.Sequence, &e.ResourceKind, &e.ResourceID, &e.Kind, &e.Revision, &e.Body); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (t *writeTx) CreateProject(p domain.Project) error {
	if p.Revision == 0 {
		p.Revision = 1
	}
	if p.PermissionsRevision == 0 {
		p.PermissionsRevision = 1
	}
	if p.Lifecycle == "" {
		p.Lifecycle = "active"
	}
	return t.exec("INSERT INTO projects(project_id,name,lifecycle,revision,permissions_revision) VALUES(?,?,?,?,?)", p.ID, p.Name, p.Lifecycle, p.Revision, p.PermissionsRevision)
}
func (t *writeTx) CreateDocument(d domain.Document) error {
	if d.LatestVersionID != "" {
		return domain.ErrTransition
	}
	if d.Revision == 0 {
		d.Revision = 1
	}
	if d.Lifecycle == "" {
		d.Lifecycle = "active"
	}
	return t.exec("INSERT INTO documents(document_id,project_id,title,lifecycle,latest_version_id,revision) VALUES(?,?,?,?,?,?)", d.ID, d.ProjectID, d.Title, d.Lifecycle, nullable(d.LatestVersionID), d.Revision)
}
func (t *writeTx) CreateContentSet(id, payloadSHA string) error {
	if !hashPattern.MatchString(payloadSHA) {
		return errors.New("invalid payload digest")
	}
	return t.exec("INSERT INTO content_sets VALUES(?,?,0)", id, payloadSHA)
}
func (t *writeTx) PutEntry(e domain.Entry) error {
	var sealed bool
	if err := t.tx.QueryRowContext(t.ctx, "SELECT sealed FROM content_sets WHERE content_set_id=?", e.ContentSetID).Scan(&sealed); err != nil {
		return notFound(err)
	}
	if sealed {
		return domain.ErrTransition
	}
	if e.Kind == "file" {
		obj, err := t.Object(e.Hash)
		if err != nil {
			return err
		}
		if obj.State != "ready" {
			return domain.ErrDeleting
		}
		if obj.Size != e.Size {
			return domain.ErrConflict
		}
	}
	if err := t.exec("INSERT INTO version_entries VALUES(?,?,?,?,?,?)", e.ContentSetID, e.Path, e.Kind, nullable(e.Hash), e.Size, nullable(e.LinkText)); err != nil {
		return err
	}
	if e.Kind == "file" {
		return t.Adopt(domain.Reference{Kind: "content_set", ID: e.ContentSetID, Hash: e.Hash}, "")
	}
	return nil
}
func (t *writeTx) PutVersion(v domain.Version) error {
	if v.Sequence != 0 {
		return domain.ErrTransition
	}
	var sequence any
	if v.Sequence != 0 {
		sequence = v.Sequence
	}
	return t.exec("INSERT INTO document_versions(version_id,document_id,sequence,base_version_id,content_set_id,title_snapshot,default_page,viewer_profile,source_basis,metadata,payload_sha256,manifest_sha256,supplied_metadata_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", v.ID, v.DocumentID, sequence, nullable(v.BaseVersionID), v.ContentSetID, v.Title, v.DefaultPage, v.ViewerProfile, v.SourceBasis, v.Metadata, v.PayloadSHA, v.ManifestSHA, nullable(v.MetadataHash))
}
func (t *writeTx) PutPage(p domain.Page) error {
	if err := t.mutableVersion(p.VersionID); err != nil {
		return err
	}
	return t.exec("INSERT INTO version_pages VALUES(?,?,?)", p.VersionID, p.Path, p.Metadata)
}
func (t *writeTx) PutRegion(r domain.Region) error {
	if err := t.mutableVersion(r.VersionID); err != nil {
		return err
	}
	return t.exec("INSERT INTO regions VALUES(?,?,?,?)", r.VersionID, r.PagePath, r.ID, r.Metadata)
}
func (t *writeTx) PutSource(s domain.Source) error {
	if err := t.mutableVersion(s.VersionID); err != nil {
		return err
	}
	return t.exec("INSERT INTO version_sources VALUES(?,?,?)", s.VersionID, s.ID, s.Metadata)
}
func (t *writeTx) PutEvidence(e domain.Evidence) error {
	if err := t.mutableVersion(e.VersionID); err != nil {
		return err
	}
	return t.exec("INSERT INTO evidence VALUES(?,?,?,?)", e.VersionID, e.ID, e.SourceID, e.Metadata)
}
func (t *writeTx) SetConfiguration(c domain.Configuration, expected int64) error {
	var current int64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT max(config_revision) FROM config_revisions").Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return domain.ErrPrecondition
	}
	if err := t.exec("INSERT INTO config_revisions VALUES(?,?,?,?)", current+1, c.Value, c.Actor, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}

	// Shortening pending secret lifetime is immediate; an already issued secret
	// never silently gains a longer lifetime from later policy expansion.
	var settings struct {
		Security domain.AuthSettings `json:"security"`
	}
	if err := json.Unmarshal(c.Value, &settings); err != nil {
		return err
	}
	if settings.Security.PairingExpirySeconds > 0 {
		if err := t.exec("UPDATE pairings SET expires_at=created_at+?*1000,revision=revision+1 WHERE consumed=0 AND state IN('pending','approved') AND expires_at>created_at+?*1000", settings.Security.PairingExpirySeconds, settings.Security.PairingExpirySeconds); err != nil {
			return err
		}
	}
	return t.appendEvent("configuration", "library", "configuration.changed", current+1, nil)
}
func (t *writeTx) PublishVersion(id string, expected int64) error {
	var documentID, title, setID string
	var sequence sql.NullInt64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT document_id,title_snapshot,content_set_id,sequence FROM document_versions WHERE version_id=?", id).Scan(&documentID, &title, &setID, &sequence); err != nil {
		return notFound(err)
	}
	if sequence.Valid {
		return domain.ErrConflict
	}
	var sealed bool
	if err := t.tx.QueryRowContext(t.ctx, "SELECT sealed FROM content_sets WHERE content_set_id=?", setID).Scan(&sealed); err != nil {
		return err
	}
	if !sealed {
		return domain.ErrTransition
	}
	var defaultPage sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, "SELECT default_page FROM document_versions WHERE version_id=?", id).Scan(&defaultPage); err != nil {
		return err
	}
	if defaultPage.Valid {
		var count int
		if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM version_pages WHERE version_id=? AND page_path=?", id, defaultPage.String).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return domain.ErrPrecondition
		}
	}
	var revision, seq int64
	var latest sql.NullString
	var base sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, "SELECT revision,latest_version_id FROM documents WHERE document_id=?", documentID).Scan(&revision, &latest); err != nil {
		return err
	}
	if revision != expected {
		return domain.ErrPrecondition
	}
	if err := t.tx.QueryRowContext(t.ctx, "SELECT base_version_id FROM document_versions WHERE version_id=?", id).Scan(&base); err != nil {
		return err
	}
	if base != latest {
		return domain.ErrPrecondition
	}
	if err := t.tx.QueryRowContext(t.ctx, "SELECT coalesce(max(sequence),0)+1 FROM document_versions WHERE document_id=?", documentID).Scan(&seq); err != nil {
		return err
	}
	var unavailable int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM version_entries e LEFT JOIN content_objects o ON e.content_hash=o.content_hash WHERE e.content_set_id=? AND e.kind='file' AND (o.content_hash IS NULL OR o.state!='ready')", setID).Scan(&unavailable); err != nil {
		return err
	}
	if unavailable != 0 {
		return domain.ErrDeleting
	}
	if err := t.exec("UPDATE document_versions SET sequence=? WHERE version_id=?", seq, id); err != nil {
		return err
	}
	if err := t.exec("UPDATE documents SET latest_version_id=?,title=?,revision=revision+1 WHERE document_id=?", id, title, documentID); err != nil {
		return err
	}
	if err := t.appendEvent("document", documentID, "version.published", revision+1, []byte(id)); err != nil {
		return err
	}
	_, err := t.EnqueueSearch(id, domain.SearchExtractorRevision)
	return err
}
func (s *Store) Entries(ctx context.Context, setID, after string, limit int) ([]domain.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, "SELECT content_set_id,relative_path,kind,coalesce(content_hash,''),byte_size,coalesce(exact_link_text,'') FROM version_entries WHERE content_set_id=? AND relative_path>? COLLATE BINARY ORDER BY relative_path COLLATE BINARY LIMIT ?", setID, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Entry{}
	for rows.Next() {
		var e domain.Entry
		if err := rows.Scan(&e.ContentSetID, &e.Path, &e.Kind, &e.Hash, &e.Size, &e.LinkText); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (t *writeTx) Object(hash string) (domain.Object, error) {
	o := domain.Object{Hash: hash}
	err := t.tx.QueryRowContext(t.ctx, "SELECT byte_size,state,coalesce(gc_token,'') FROM content_objects WHERE content_hash=?", hash).Scan(&o.Size, &o.State, &o.GCToken)
	return o, notFound(err)
}
func (t *writeTx) PutDurableObject(o domain.Object) error {
	if !hashPattern.MatchString(o.Hash) || o.Size < 0 {
		return errors.New("invalid durable object identity")
	}
	existing, err := t.Object(o.Hash)
	if err == nil {
		if existing.State == "deleting" {
			return domain.ErrDeleting
		}
		if existing.Size != o.Size {
			return domain.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return t.exec("INSERT INTO content_objects VALUES(?,?,'ready',NULL)", o.Hash, o.Size)
}
func (t *writeTx) AcquirePin(p domain.Pin) error {
	object, err := t.Object(p.Hash)
	if err != nil {
		return err
	}
	if object.State != "ready" {
		return domain.ErrDeleting
	}
	var existing domain.Pin
	err = t.tx.QueryRowContext(t.ctx, "SELECT pin_id,owner_kind,owner_id,content_hash FROM object_pins WHERE pin_id=?", p.ID).Scan(&existing.ID, &existing.Kind, &existing.OwnerID, &existing.Hash)
	if err == nil {
		if existing != p {
			return domain.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return t.exec("INSERT INTO object_pins VALUES(?,?,?,?)", p.ID, p.Kind, p.OwnerID, p.Hash)
}
func (t *writeTx) ReleasePin(id string) error {
	return t.exec("DELETE FROM object_pins WHERE pin_id=?", id)
}
func (t *writeTx) Adopt(ref domain.Reference, stagingPin string) error {
	o, err := t.Object(ref.Hash)
	if err != nil {
		return err
	}
	if o.State != "ready" {
		return domain.ErrDeleting
	}
	if stagingPin != "" {
		var hash, ownerKind, ownerID string
		if err := t.tx.QueryRowContext(t.ctx, "SELECT content_hash,owner_kind,owner_id FROM object_pins WHERE pin_id=?", stagingPin).Scan(&hash, &ownerKind, &ownerID); err != nil {
			return notFound(err)
		}
		if hash != ref.Hash {
			return domain.ErrConflict
		}
		if ownerKind == "upload" {
			u, err := upload(t.ctx, t.tx, ownerID)
			if err != nil {
				return err
			}
			if u.State != "sealed" {
				return domain.ErrTransition
			}
		}
	}
	if err := t.exec("INSERT OR IGNORE INTO object_references VALUES(?,?,?)", ref.Kind, ref.ID, ref.Hash); err != nil {
		return err
	}
	if stagingPin != "" {
		return t.ReleasePin(stagingPin)
	}
	return nil
}
func (t *writeTx) ReserveGC(hash, token string) (domain.GCReservation, error) {
	r := domain.GCReservation{Hash: hash, Token: token}
	if token == "" {
		return r, errors.New("GC reservation token required")
	}
	object, err := t.Object(hash)
	if err != nil {
		return r, err
	}
	if object.State != "ready" {
		return r, domain.ErrDeleting
	}
	var capturing int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM copy_sets WHERE stage='capturing' OR json_extract(value,'$.Capturing')=1`).Scan(&capturing); err != nil {
		return r, err
	}
	if capturing > 0 {
		return r, domain.ErrPinned
	}
	var pins, refs, owners int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT (SELECT count(*) FROM object_pins WHERE content_hash=?),(SELECT count(*) FROM object_references WHERE content_hash=?),(SELECT count(*) FROM version_entries WHERE content_hash=?)+(SELECT count(*) FROM contexts WHERE content_hash=?)+(SELECT count(*) FROM metadata_indexes WHERE metadata_hash=?)+(SELECT count(*) FROM document_versions WHERE manifest_sha256=? OR supplied_metadata_hash=?)`, hash, hash, hash, hash, hash, hash, hash).Scan(&pins, &refs, &owners); err != nil {
		return r, err
	}
	if pins+refs+owners != 0 {
		return r, domain.ErrPinned
	}
	return r, t.exec("UPDATE content_objects SET state='deleting',gc_token=? WHERE content_hash=?", token, hash)
}
func (t *writeTx) FinishGC(r domain.GCReservation) error {
	var token sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, "SELECT gc_token FROM content_objects WHERE content_hash=? AND state='deleting'", r.Hash).Scan(&token); err != nil {
		return notFound(err)
	}
	if !token.Valid || token.String != r.Token {
		return domain.ErrConflict
	}
	return t.exec("DELETE FROM content_objects WHERE content_hash=?", r.Hash)
}

var _ app.Store = (*Store)(nil)
var _ app.WriteTx = (*writeTx)(nil)

func receiptState(status int) string {
	if status >= 400 {
		return "failed"
	}
	return "completed"
}
func (t *writeTx) ReconcileOperation(key domain.OperationKey) error {
	return t.exec("UPDATE operation_receipts SET state='reconciling' WHERE principal_id=? AND operation_id=? AND state='accepted'", key.PrincipalID, key.OperationID)
}

func (s *Store) ReferencedObjects(ctx context.Context, after string, limit int) ([]domain.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, "SELECT content_hash,byte_size,state,coalesce(gc_token,'') FROM content_objects o WHERE content_hash>? AND (EXISTS(SELECT 1 FROM object_pins p WHERE p.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM object_references r WHERE r.content_hash=o.content_hash)) ORDER BY content_hash LIMIT ?", after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Object{}
	for rows.Next() {
		var o domain.Object
		if err := rows.Scan(&o.Hash, &o.Size, &o.State, &o.GCToken); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

func (t *writeTx) SealContentSet(id string) error {
	var unavailable int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM version_entries e LEFT JOIN content_objects o ON e.content_hash=o.content_hash WHERE e.content_set_id=? AND e.kind='file' AND (o.content_hash IS NULL OR o.state!='ready')", id).Scan(&unavailable); err != nil {
		return err
	}
	if unavailable != 0 {
		return domain.ErrDeleting
	}
	result, err := t.tx.ExecContext(t.ctx, "UPDATE content_sets SET sealed=1 WHERE content_set_id=?", id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrNotFound
	}
	return nil
}
func (t *writeTx) mutableVersion(id string) error {
	var sequence sql.NullInt64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT sequence FROM document_versions WHERE version_id=?", id).Scan(&sequence); err != nil {
		return notFound(err)
	}
	if sequence.Valid {
		return domain.ErrTransition
	}
	return nil
}
