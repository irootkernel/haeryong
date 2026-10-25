package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"strconv"
)

type normalization struct {
	DocumentID    string   `json:"document_id"`
	VersionID     string   `json:"version_id"`
	BaseVersionID *string  `json:"base_version_id"`
	CreatedAt     string   `json:"created_at"`
	Title         string   `json:"title_snapshot"`
	Omitted       []string `json:"omitted_fields"`
	Schema        string   `json:"schema_version"`
	Normalizer    string   `json:"normalizer_version"`
	ProjectID     string   `json:"project_id"`
}

func (s *Service) metadata(ctx context.Context, token, key string, input ImportRequest, pinID string) (domain.Object, error) {
	if input.Metadata != nil && input.MetadataRef != nil {
		return domain.Object{}, sec.ErrInvalid
	}
	if input.MetadataRef != nil {
		var obj domain.Object
		e := s.Store.Write(ctx, func(tx app.WriteTx) error {
			u, e := tx.Upload(input.MetadataRef.ID)
			if e != nil {
				return e
			}
			if e := s.current(ctx, tx, token, u); e != nil {
				return e
			}
			if u.Purpose != "metadata" || u.ObjectHash != input.MetadataRef.Hash || u.Checkpoint.AcceptedBytes != input.MetadataRef.Size {
				return sec.ErrInvalid
			}
			var target struct {
				OperationID string  `json:"operation_id"`
				Action      string  `json:"action"`
				ProjectID   string  `json:"project_id"`
				DocumentID  *string `json:"document_id"`
			}
			if json.Unmarshal(u.Target, &target) != nil {
				return sec.ErrInvalid
			}
			if target.OperationID != key || target.Action != "import" || target.DocumentID == nil != (input.DocumentID == nil) {
				return sec.ErrInvalid
			}
			if target.DocumentID != nil && *target.DocumentID != *input.DocumentID {
				return sec.ErrInvalid
			}
			size, e := strconv.ParseInt(u.Checkpoint.AcceptedBytes, 10, 64)
			if e != nil {
				return sec.ErrInvalid
			}
			obj = domain.Object{Hash: u.ObjectHash, Size: size}
			return nil
		})
		return obj, e
	}
	if input.Metadata == nil {
		return domain.Object{}, nil
	}
	return s.Bytes.Put(ctx, input.Metadata, func(tx app.WriteTx, o domain.Object) error {
		_, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		return tx.AcquirePin(domain.Pin{ID: pinID, Kind: "import", OwnerID: key, Hash: o.Hash})
	})
}
func (s *Service) Import(ctx context.Context, token, key string, input ImportRequest) (domain.Response, error) {
	record, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		return s.Auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{Key: domain.OperationKey{PrincipalID: record.PrincipalID}, Method: "POST", Target: "/api/v1/imports", Body: []byte(`{}`)})
	}); e != nil {
		return domain.Response{}, e
	}
	if !input.DocumentPresent || !idPattern.MatchString(input.UploadID) || (input.DocumentID != nil && !idPattern.MatchString(*input.DocumentID)) || (input.ProjectID != "" && !idPattern.MatchString(input.ProjectID)) || (input.VersionID != "" && !idPattern.MatchString(input.VersionID)) || (input.BaseVersionID != nil && !idPattern.MatchString(*input.BaseVersionID)) {
		return domain.Response{}, sec.ErrInvalid
	}
	pinID, e := service.NewID()
	if e != nil {
		return domain.Response{}, e
	}
	pinID = "metadata-request:" + pinID
	if input.Metadata != nil {
		defer s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin(pinID) })
	}
	metadata, e := s.metadata(ctx, token, key, input, pinID)
	if e != nil {
		return domain.Response{}, e
	}
	_, e = s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	var u domain.Upload
	var payload string
	var target struct {
		ProjectID  string  `json:"project_id"`
		DocumentID *string `json:"document_id"`
		DraftID    *string `json:"draft_id"`
	}
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		u, e = tx.Upload(input.UploadID)
		if e != nil {
			return e
		}
		if e := s.current(ctx, tx, token, u); e != nil {
			return e
		}
		if u.Purpose != "result" {
			return sec.ErrInvalid
		}
		if u.ContentSetID == "" {
			return domain.ErrTransition
		}
		if json.Unmarshal(u.Target, &target) != nil || target.DraftID != nil {
			return sec.ErrInvalid
		}
		if (target.DocumentID == nil) != (input.DocumentID == nil) || (target.DocumentID != nil && *target.DocumentID != *input.DocumentID) || (input.ProjectID != "" && input.ProjectID != target.ProjectID) {
			return sec.ErrInvalid
		}
		if input.MetadataRef != nil {
			m, e := tx.Upload(input.MetadataRef.ID)
			if e != nil {
				return e
			}
			var mt struct {
				ProjectID  string  `json:"project_id"`
				DocumentID *string `json:"document_id"`
			}
			if json.Unmarshal(m.Target, &mt) != nil || mt.ProjectID != target.ProjectID || value(mt.DocumentID) != value(target.DocumentID) {
				return sec.ErrInvalid
			}
		}
		var sealed bool
		payload, sealed, e = tx.ContentSet(u.ContentSetID)
		if e != nil {
			return e
		}
		if !sealed {
			return domain.ErrTransition
		}
		return nil
	})
	if e != nil {
		return domain.Response{}, e
	}
	body := map[string]any{"target": json.RawMessage(u.Target), "payload_sha256": payload, "metadata_sha256": metadata.Hash, "document_id": input.DocumentID, "project_id": input.ProjectID, "version_id": input.VersionID, "base_version_id": input.BaseVersionID}
	r, e := s.request(ctx, token, key, "POST", "/api/v1/imports", body)
	if e != nil {
		return domain.Response{}, e
	}
	for field, omitted := range map[string]bool{"project_id": input.ProjectID == "", "version_id": input.VersionID == "", "base_version_id": !input.BasePresent, "metadata": metadata.Hash == ""} {
		if omitted {
			r.Omitted = append(r.Omitted, field)
		}
	}

	// Resolve saved semantic markers under current target authority before reopening
	// metadata bytes. A completed receipt survives later staged-byte reclamation.
	var saved bool
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, r); e != nil {
			return e
		}
		receipt, e := tx.Receipt(r.Key)
		if errors.Is(e, domain.ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		var original map[string]json.RawMessage
		if e = json.Unmarshal(receipt.Original.Body, &original); e != nil {
			return e
		}
		if _, used := original["selection_name_used_for_default"]; used {
			body["selection_name_used_for_default"] = u.SelectionName
		}
		saved = true
		return nil
	})
	if e != nil {
		return domain.Response{}, e
	}
	if saved {
		r.Body = encoded(body)
		if replay, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), r); e != nil || found {
			return replay, e
		}
	}
	preparation, e := s.prepareMetadata(ctx, token, metadata, u.ContentSetID)
	if e != nil {
		return domain.Response{}, e
	}
	defer s.releaseMetadataPreparation(preparation)
	var hints metadataHints
	if metadata.Hash != "" {
		hints, e = s.metadataHints(ctx, token, preparation, metadata)
		if e != nil {
			return domain.Response{}, e
		}
	}
	delete(body, "selection_name_used_for_default")
	if input.DocumentID == nil && hints.Title == nil {
		body["selection_name_used_for_default"] = u.SelectionName
	}
	r.Body = encoded(body)
	return app.ExecuteNormalized(ctx, s.Store, s.Auth.Authority(token), r, func(tx app.WriteTx) ([]byte, error) {
		n := normalization{DocumentID: "", VersionID: input.VersionID, BaseVersionID: input.BaseVersionID, CreatedAt: s.now(), Omitted: r.Omitted, Schema: "1.0", Normalizer: "1.0", ProjectID: target.ProjectID, Title: u.SelectionName}
		if input.DocumentID == nil {
			if input.BaseVersionID != nil {
				return nil, sec.ErrInvalid
			}
			n.DocumentID, e = service.NewID()
		} else {
			n.DocumentID = *input.DocumentID
			d, err := tx.Document(n.DocumentID)
			if err != nil {
				return nil, err
			}
			n.Title = d.Title
			if input.BasePresent && input.BaseVersionID == nil {
				return nil, sec.ErrInvalid
			}
			if !input.BasePresent {
				n.BaseVersionID = strptr(d.LatestVersionID)
			}
		}
		if e != nil {
			return nil, e
		}
		if n.VersionID == "" {
			n.VersionID, e = service.NewID()
		}
		if e != nil {
			return nil, e
		}
		if hints.Title != nil {
			n.Title = *hints.Title
		}
		if hints.DocumentID != nil && *hints.DocumentID != n.DocumentID {
			return nil, invalidMeta("/document/id")
		}
		if hints.ProjectID != nil && *hints.ProjectID != n.ProjectID {
			return nil, invalidMeta("/document/project_id")
		}
		if hints.VersionID != nil && *hints.VersionID != n.VersionID {
			return nil, invalidMeta("/version/id")
		}
		if hints.BasePresent && value(hints.BaseVersionID) != value(n.BaseVersionID) {
			return nil, invalidMeta("/version/base_version_id")
		}
		return encoded(n), nil
	}, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		live, err := tx.Upload(u.ID)
		if err != nil {
			return domain.Response{}, err
		}
		if live.State != "sealed" || live.ContentSetID != u.ContentSetID {
			return domain.Response{}, domain.ErrTransition
		}
		if e := targetAuthority(tx, u.Purpose, u.Target); e != nil {
			return domain.Response{}, e
		}
		if e := validatePreparedMetadata(tx, preparation, metadata, u.ContentSetID, r.Key.PrincipalID); e != nil {
			return domain.Response{}, e
		}
		if input.MetadataRef != nil {
			m, err := tx.Upload(input.MetadataRef.ID)
			if err != nil {
				return domain.Response{}, err
			}
			if m.State != "sealed" || m.ObjectHash != metadata.Hash {
				return domain.Response{}, domain.ErrTransition
			}
			var mt struct {
				OperationID string  `json:"operation_id"`
				Action      string  `json:"action"`
				ProjectID   string  `json:"project_id"`
				DocumentID  *string `json:"document_id"`
			}
			if json.Unmarshal(m.Target, &mt) != nil || mt.Action != "import" || mt.OperationID != key || mt.ProjectID != target.ProjectID || (mt.DocumentID == nil) != (input.DocumentID == nil) || (mt.DocumentID != nil && *mt.DocumentID != *input.DocumentID) {
				return domain.Response{}, sec.ErrInvalid
			}
		}
		var n normalization
		if json.Unmarshal(receipt.Normalization, &n) != nil {
			return domain.Response{}, sec.ErrInvalid
		}
		revision := int64(1)
		if !(input.DocumentID == nil) {
			d, e := tx.Document(n.DocumentID)
			if e != nil {
				return domain.Response{}, e
			}
			revision = d.Revision
			if d.LatestVersionID != value(n.BaseVersionID) {
				return domain.Response{}, ErrVersionConflict
			}
		}
		if gone, err := tx.TombstonedVersion(n.VersionID); err != nil {
			return domain.Response{}, err
		} else if gone {
			return domain.Response{}, ErrImmutableConflict
		}
		if input.VersionID != "" {
			old, err := tx.PublishedVersion(input.VersionID)
			if err == nil {
				if old.DocumentID != n.DocumentID || old.PayloadSHA != payload || old.MetadataHash != metadata.Hash {
					return domain.Response{}, ErrImmutableConflict
				}
				return domain.Response{Status: 200, Body: encoded(map[string]string{"version_id": old.ID, "document_id": old.DocumentID})}, nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return domain.Response{}, err
			}
		}
		scope, e := tx.Scope(n.ProjectID)
		if e != nil {
			return domain.Response{}, e
		}
		library, e := tx.Library()
		if e != nil {
			return domain.Response{}, e
		}
		job, e := service.NewID()
		if e != nil {
			return domain.Response{}, e
		}
		intent := domain.ImportIntent{JobID: job, PrincipalID: r.Key.PrincipalID, UploadID: u.ID, ContentSetID: u.ContentSetID, PayloadSHA: payload, MetadataHash: metadata.Hash, ProjectID: n.ProjectID, DocumentID: n.DocumentID, VersionID: n.VersionID, BaseVersionID: value(n.BaseVersionID), Title: n.Title, CreatedAt: n.CreatedAt, AuthEpoch: library.AuthEpoch, NewDocument: input.DocumentID == nil, DocumentRevision: revision, PermissionsRevision: scope.Project.PermissionsRevision, ConfigRevision: scope.ConfigRevision, Normalization: receipt.Normalization}
		if e := tx.CreateJob(domain.Job{ID: job, Kind: "import", UploadID: u.ID, Scope: encoded(map[string]any{"principal_id": r.Key.PrincipalID, "auth_epoch": library.AuthEpoch, "purpose": "result", "target": json.RawMessage(u.Target)}), ConfigRevision: scope.ConfigRevision, Permissions: map[string]int64{n.ProjectID: scope.Project.PermissionsRevision}}); e != nil {
			return domain.Response{}, e
		}
		if preparation != "" {
			if e := tx.LinkMetadataIndex(preparation, job); e != nil {
				return domain.Response{}, e
			}
		}
		if e := tx.PinContentSet(job, u.ContentSetID); e != nil {
			return domain.Response{}, e
		}
		if e := tx.PutImportIntent(intent); e != nil {
			return domain.Response{}, e
		}
		if metadata.Hash != "" {
			if e := tx.AcquirePin(domain.Pin{ID: "import-metadata:" + job, Kind: "job", OwnerID: job, Hash: metadata.Hash}); e != nil {
				return domain.Response{}, e
			}
		}
		return domain.Response{Status: 202, Body: encoded(map[string]string{"operation_id": key, "job_id": job})}, nil
	})
}
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (s *Service) Validate(ctx context.Context, token, key string, input ImportRequest) (domain.Response, error) {
	_, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	var upload domain.Upload
	var payload string
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		upload, e = s.authorizedUpload(ctx, tx, token, input.UploadID)
		if e != nil {
			return e
		}
		if upload.Purpose != "result" {
			return sec.ErrInvalid
		}
		if upload.ContentSetID == "" {
			return domain.ErrTransition
		}
		payload, _, e = tx.ContentSet(upload.ContentSetID)
		return e
	})
	if e != nil {
		return domain.Response{}, e
	}
	temp, e := service.NewID()
	if e != nil {
		return domain.Response{}, e
	}
	var metadata domain.Object
	if input.Metadata != nil && input.MetadataRef != nil {
		return domain.Response{}, sec.ErrInvalid
	}
	if input.Metadata != nil {
		defer s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin("metadata-request:" + temp) })
		metadata, e = s.metadata(ctx, token, temp, input, "metadata-request:"+temp)
	} else if input.MetadataRef != nil {
		e = s.Store.Write(ctx, func(tx app.WriteTx) error {
			m, e := s.authorizedUpload(ctx, tx, token, input.MetadataRef.ID)
			if e != nil {
				return e
			}
			var result map[string]any
			var target map[string]any
			_ = json.Unmarshal(upload.Target, &result)
			_ = json.Unmarshal(m.Target, &target)
			if m.Purpose != "metadata" || target["action"] != "import" || target["project_id"] != result["project_id"] || target["document_id"] != result["document_id"] || m.ObjectHash != input.MetadataRef.Hash || m.Checkpoint.AcceptedBytes != input.MetadataRef.Size {
				return sec.ErrInvalid
			}
			size, e := strconv.ParseInt(m.Checkpoint.AcceptedBytes, 10, 64)
			if e != nil {
				return sec.ErrInvalid
			}
			metadata = domain.Object{Hash: m.ObjectHash, Size: size}
			return nil
		})
	}
	if e != nil {
		return domain.Response{}, e
	}
	body := map[string]any{"target": json.RawMessage(upload.Target), "payload_sha256": payload, "metadata_sha256": metadata.Hash}
	var request domain.Request
	if key != "" {
		request, e = s.request(ctx, token, key, "POST", "/api/v1/packages/validate", body)
		if e != nil {
			return domain.Response{}, e
		}
		if replay, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), request); e != nil || found {
			return replay, e
		}
	}
	if upload.State != "sealed" {
		return domain.Response{}, domain.ErrTransition
	}
	available, reason, e := s.available(ctx, token, upload.ID)
	if e != nil {
		return domain.Response{}, e
	}
	if !available {
		_ = reason
		return domain.Response{}, ErrPackageInvalid
	}
	preparation, e := s.prepareMetadata(ctx, token, metadata, upload.ContentSetID)
	if e != nil {
		return domain.Response{}, e
	}
	defer s.releaseMetadataPreparation(preparation)
	if metadata.Hash != "" {
		if _, e := s.metadataHints(ctx, token, preparation, metadata); e != nil {
			return domain.Response{}, e
		}
	}
	response := domain.Response{Status: 200, Body: encoded(map[string]any{"valid": true, "upload_id": upload.ID, "payload_sha256": payload, "checkpoint": upload.Checkpoint})}
	if key == "" {
		if e := s.Store.Write(ctx, func(tx app.WriteTx) error {
			record, e := s.Auth.AuthenticateWrite(ctx, tx, token)
			if e != nil {
				return e
			}
			live, e := s.authorizedUpload(ctx, tx, token, upload.ID)
			if e != nil {
				return e
			}
			if live.State != "sealed" || live.ContentSetID != upload.ContentSetID {
				return domain.ErrTransition
			}
			return validatePreparedMetadata(tx, preparation, metadata, upload.ContentSetID, record.PrincipalID)
		}); e != nil {
			return domain.Response{}, e
		}
		return response, nil
	}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		live, e := s.authorizedUpload(ctx, tx, token, upload.ID)
		if e != nil {
			return domain.Response{}, e
		}
		if live.State != "sealed" {
			return domain.Response{}, domain.ErrTransition
		}
		if input.MetadataRef != nil {
			m, e := s.authorizedUpload(ctx, tx, token, input.MetadataRef.ID)
			if e != nil {
				return domain.Response{}, e
			}
			if m.State != "sealed" {
				return domain.Response{}, domain.ErrTransition
			}
		}
		if e := validatePreparedMetadata(tx, preparation, metadata, upload.ContentSetID, request.Key.PrincipalID); e != nil {
			return domain.Response{}, e
		}
		return response, nil
	})
}

func normalizedVersion(i domain.ImportIntent, defaultPage *string) domain.Version {
	b := encoded(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "source_basis": "unverified", "document": map[string]any{"id": i.DocumentID, "project_id": i.ProjectID, "title": i.Title}, "version": map[string]any{"id": i.VersionID, "base_version_id": strptr(i.BaseVersionID), "source_intent": nil, "created_at": i.CreatedAt}, "content": map[string]any{"default_page": defaultPage, "inventory": "/api/v1/versions/" + i.VersionID + "/files", "payload_sha256": i.PayloadSHA}})
	hash := sha256.Sum256(b)
	return domain.Version{ID: i.VersionID, DocumentID: i.DocumentID, BaseVersionID: i.BaseVersionID, ContentSetID: i.ContentSetID, Title: i.Title, DefaultPage: defaultPage, ViewerProfile: "html_js_v1", SourceBasis: "unverified", Metadata: b, PayloadSHA: i.PayloadSHA, ManifestSHA: hex.EncodeToString(hash[:]), MetadataHash: i.MetadataHash}
}
func (s *Service) publish(ctx context.Context, j domain.Job) error {
	var intent domain.ImportIntent
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; intent, e = tx.ImportIntent(j.ID); return e })
	if e != nil {
		return e
	}
	v := normalizedVersion(intent, nil)
	if intent.MetadataHash != "" {
		if e := s.indexMetadata(ctx, j, intent, &v); e != nil {
			return e
		}
	}
	generated, e := s.generated(ctx, j, intent, v)
	if e != nil {
		return e
	}
	v.ManifestSHA = generated.Hash
	summary := normalizedVersion(intent, v.DefaultPage)
	var summaryValue map[string]any
	_ = json.Unmarshal(summary.Metadata, &summaryValue)
	summaryValue["source_basis"] = v.SourceBasis
	summaryValue["document"].(map[string]any)["title"] = v.Title
	v.Metadata = encoded(summaryValue)
	intent.Title = v.Title

	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := s.jobFence(tx, j); e != nil {
			return e
		}
		scope, e := tx.Scope(intent.ProjectID)
		if e != nil {
			return e
		}
		if scope.Project.PermissionsRevision != intent.PermissionsRevision {
			return domain.ErrDenied
		}
		if !intent.NewDocument {
			d, e := tx.Document(intent.DocumentID)
			if e != nil {
				return e
			}
			if d.LatestVersionID != intent.BaseVersionID {
				return ErrVersionConflict
			}
			if e := app.CheckLifecycleApplication(tx, intent.DocumentID, intent.DocumentRevision); e != nil {
				return e
			}
		}
		if gone, e := tx.TombstonedVersion(v.ID); e != nil {
			return e
		} else if gone {
			return ErrImmutableConflict
		}
		if old, e := tx.PublishedVersion(v.ID); e == nil {
			if old.DocumentID != v.DocumentID || old.PayloadSHA != v.PayloadSHA || old.MetadataHash != v.MetadataHash {
				return ErrImmutableConflict
			}
			return tx.FinishJob(j.ID, j.AttemptGeneration, "", func(app.WriteTx) error {
				return tx.SetJobResult(j.ID, encoded(map[string]any{"version_id": v.ID, "document_id": v.DocumentID, "status": 200}))
			})
		} else if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		return tx.FinishJob(j.ID, j.AttemptGeneration, "", func(app.WriteTx) error {
			if e := tx.FinalizeImport(intent, v); e != nil {
				return e
			}
			if e := tx.SetJobResult(j.ID, encoded(map[string]any{"version_id": v.ID, "document_id": v.DocumentID, "status": 201})); e != nil {
				return e
			}
			if e := tx.ReleasePin("manifest:" + j.ID); e != nil {
				return e
			}
			if e := tx.ReleasePin("import-metadata:" + j.ID); e != nil {
				return e
			}
			return tx.ReleaseOwnerPins("job", j.ID)
		})
	})
}
