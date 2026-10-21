package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

var (
	shaPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// DefaultCheckBound bounds one detached check so a vanished caller cannot
// strand a pending receipt (C7); CheckBound overrides it in tests (D4).
const DefaultCheckBound = 2 * time.Minute

// locationBody is the bounded byte target of an evidence item (path plus an
// optional inclusive 1-based line range; zero means the whole file).
type locationBody struct {
	Path      string `json:"path"`
	LineStart int64  `json:"line_start,omitempty"`
	LineEnd   int64  `json:"line_end,omitempty"`
}

type sourceIdentity struct {
	Kind          string `json:"kind"`
	RetentionMode string `json:"retention_mode"`
	RepoID        string `json:"repo_id"`
	ObjectFormat  string `json:"object_format"`
	ExactRevision string `json:"exact_revision"`
	SnapshotSHA   string `json:"snapshot_sha256"`
	AssetPath     string `json:"asset_path"`
}

// verifiedEvidence carries the immutable authored inputs of one check. A
// linkage failure (SN1) is recorded here and becomes an unavailable outcome,
// never a stage-1 request error.
type verifiedEvidence struct {
	evidence        domain.Evidence
	source          domain.Source
	identity        sourceIdentity
	location        locationBody
	mapping         Mapping
	mappingSourceID string
	entry           domain.Entry
	retained        bool
	linkageFailure  string
}

// targetsJSON is the registry per-source entry shape (C13): the identity of
// the pinned target only; the outcome stays on the verification row.
func (ve verifiedEvidence) targetsJSON() []byte {
	if ve.identity.Kind == "git" {
		return encoded(map[string]any{"sources": []any{map[string]any{"source_id": nullableJSON(ve.mappingSourceID), "kind": "git", "repo_id": ve.identity.RepoID, "object_format": ve.identity.ObjectFormat, "exact_revision": ve.identity.ExactRevision}}})
	}
	return encoded(map[string]any{"sources": []any{map[string]any{"source_id": nil, "kind": "snapshot", "snapshot_sha256": ve.identity.SnapshotSHA}}})
}

// requestedSelection follows registry ~L151 (C13): project id, the
// configuration source ids (never the version-local metadata id), mode
// pinned, and the requested revision or snapshot selection.
func (ve verifiedEvidence) requestedSelection(projectID string) []byte {
	selection := map[string]any{"project_id": projectID, "mode": "pinned"}
	if ve.identity.Kind == "git" {
		selection["source_ids"] = []string{ve.mappingSourceID}
		selection["revision"] = ve.identity.ExactRevision
	} else {
		selection["source_ids"] = []string{}
		selection["snapshot_sha256"] = ve.identity.SnapshotSHA
	}
	return encoded(selection)
}

func nullableJSON(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type expectations struct {
	digest string
	sha    string
	length int64
	ref    string
	inline []byte
}

type reservation struct {
	intentID            string
	operation           string
	targetSetID         string
	expectationByteSize int64
	versionID           string
	evidenceID          string
	capturedAt          string
	evidence            verifiedEvidence
	expectations        expectations
	digestExpected      string
	configRevision      int64
	permissions         int64
	principalID         string
	pinID               string
}

// checkOutcome is the durable result computed outside the DB lock.
type checkOutcome struct {
	verificationID string
	readID         string
	targetSetID    string
	bytesSHA       string
	bytesOrigin    string
	outcome        string
	reason         string
}

// Verify runs the staged §7.3 protocol for one imported-evidence check. The
// canonical receipt identity is the query-form route with the normalized
// expectation body; the catalog path form normalizes to it. After a successful
// reservation the check runs under a context detached from the request (C7),
// so a vanished caller leaves a terminal receipt instead of a pending one.
func (s *Service) Verify(ctx context.Context, token, versionID, evidenceID, key string, rawBody []byte) (domain.Response, error) {
	if !uuidPattern.MatchString(key) {
		return domain.Response{}, sec.ErrInvalid
	}
	res, response, done, err := s.reserveVerification(ctx, token, versionID, evidenceID, key, rawBody)
	if done || err != nil {
		return response, err
	}
	bound := s.CheckBound
	if bound <= 0 {
		bound = DefaultCheckBound
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bound)
	defer cancel()
	if err := s.fault("after_reservation"); err != nil {
		response, err := s.settleFailed(checkCtx, res, 503, "SERVICE_RECOVERING", "injected_after_reservation")
		return response, err
	}
	if res.expectations.ref != "" && res.expectationByteSize > 0 {
		if err := s.validateExpectationUTF8(checkCtx, res); err != nil {
			if errors.Is(err, errExpectationNotUTF8) {
				// G10: genuinely invalid UTF-8 settles as 400 with no
				// verification row, the intent resolved and the pins freed.
				response, err := s.settleFailed(context.WithoutCancel(checkCtx), res, 400, "INVALID_REQUEST", "expected_excerpt_ref_not_utf8")
				return response, err
			}
			// I/O and integrity errors stay distinct from source reads.
			response, err := s.settleFailed(context.WithoutCancel(checkCtx), res, 503, "FILE_ACCESS_ERROR", "expectation_read_failed")
			return response, err
		}
	}
	outcome, err := s.readTarget(checkCtx, res)
	if err != nil {
		if checkCtx.Err() != nil {
			// The detached bound expired; settle typed rather than strand.
			response, err := s.settleFailed(context.WithoutCancel(checkCtx), res, 503, "SERVICE_RECOVERING", "check_bound_exceeded")
			return response, err
		}
		// G14: the git-binary reason only for a genuine git failure; every
		// other read error (including retained snapshots) is kind-neutral.
		reason := "source_read_failed"
		if errors.Is(err, ErrGitUnavailable) {
			reason = "git_unavailable"
		}
		response, err := s.settleFailed(context.WithoutCancel(checkCtx), res, 503, "FILE_ACCESS_ERROR", reason)
		return response, err
	}
	if err := s.fault("after_pins"); err != nil {
		response, err := s.settleFailed(checkCtx, res, 503, "SERVICE_RECOVERING", "injected_after_pins")
		return response, err
	}
	response, err = s.commitVerification(checkCtx, token, key, res, outcome)
	if err != nil && ctx.Err() != nil {
		// The work committed under the detached context; only the delivery
		// failed. The retry replays the terminal receipt.
		return domain.Response{}, ctx.Err()
	}
	return response, err
}

// reserveVerification is stage 1: authenticate, authorize, reserve the
// receipt, and fix and persist the pinned target plus the check intent.
func (s *Service) reserveVerification(ctx context.Context, token, versionID, evidenceID, key string, rawBody []byte) (res reservation, response domain.Response, done bool, err error) {
	target := CanonicalTarget(versionID, evidenceID)
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		el, e := Check(tx, actor, versionID)
		if e != nil {
			return e
		}
		if !el.MayReadSource() {
			return domain.ErrDenied
		}
		ve, e := s.loadEvidence(tx, el, evidenceID)
		if e != nil {
			return e
		}
		normalized, expectations, e := s.validateBody(tx, actor.PrincipalID, key, versionID, evidenceID, actor.Scope.Kind == "owner", rawBody)
		if e != nil {
			return e
		}
		request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: target, Body: normalized}
		if e := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, request); e != nil {
			return e
		}
		receipt, existing, e := tx.ReserveOperation(request, nil)
		if e != nil {
			return e
		}
		if existing {
			switch receipt.State {
			case "completed", "failed":
				response = *receipt.Response
				done = true
				return nil
			case "reconciling":
				return domain.ErrReconciliation
			}
			response, done = PendingResponse(key), true
			return nil
		}
		state, e := tx.Scope(el.ProjectID)
		if e != nil {
			return e
		}
		res = reservation{
			intentID:       mustID(),
			operation:      key,
			targetSetID:    mustID(),
			versionID:      versionID,
			evidenceID:     evidenceID,
			capturedAt:     s.now(),
			evidence:       ve,
			expectations:   expectations,
			digestExpected: expectations.digest,
			configRevision: state.ConfigRevision,
			permissions:    state.Project.PermissionsRevision,
			principalID:    actor.PrincipalID,
		}
		// The reservation-time target set records the accepted declared
		// identity (plan T2). It becomes a selection candidate only when a
		// completed record references it (addendum r4.1 rule 4). S17: no
		// target was determined when linkage failed — never fabricate one.
		if ve.linkageFailure == "" {
			if e := tx.PutTargetSet(domain.TargetSet{ID: res.targetSetID, ProjectID: el.ProjectID, RequestedSelection: ve.requestedSelection(el.ProjectID), TargetsJSON: ve.targetsJSON(), CapturedAt: res.capturedAt}); e != nil {
				return e
			}
		}
		intent := domain.CheckIntent{ID: res.intentID, OperationID: key, PrincipalID: actor.PrincipalID, Kind: "verification", CanonicalTarget: encoded(map[string]string{"route": target, "version_id": versionID, "evidence_id": evidenceID}), ValidatedBody: normalized, ExpectationUpload: expectations.ref, ConfigRevision: res.configRevision, PermissionsRevision: res.permissions, State: "open", CreatedAt: res.capturedAt}
		if e := tx.PutCheckIntent(intent); e != nil {
			return e
		}
		// C6: pin the referenced expectation object to the intent from
		// reservation to commit; remember its size for the streaming UTF-8
		// validation (G10).
		if expectations.ref != "" && expectations.sha != "" {
			object, e := tx.Object(expectations.sha)
			if e != nil {
				return e
			}
			res.expectationByteSize = object.Size
			res.pinID = mustID()
			return tx.AcquirePin(domain.Pin{ID: res.pinID, Kind: domain.CheckIntentPinOwner, OwnerID: res.intentID, Hash: expectations.sha})
		}
		return nil
	})
	return res, response, done, err
}

func mustID() string {
	id, _ := service.NewID()
	return id
}

// validateBody decodes the closed expectation set and normalizes retry
// identity to the expectation byte digest and length, so inline and
// referenced forms with identical bytes share one receipt identity (D12).
// For a referenced expectation the digest resolves from the sealed upload,
// or — on replay after the upload became unreadable (SN7) — from the
// completed verification of this same principal and operation key, proven
// against the original intent's recorded upload id (C6).
func (s *Service) validateBody(tx app.WriteTx, principalID, key, versionID, evidenceID string, owner bool, raw []byte) ([]byte, expectations, error) {
	if len(raw) == 0 {
		return nil, expectations{}, sec.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, expectations{}, sec.ErrInvalid
	}
	for name := range fields {
		if name != "expected_bytes_sha256" && name != "expected_excerpt" && name != "expected_excerpt_ref" {
			return nil, expectations{}, sec.ErrInvalid
		}
	}
	out := expectations{}
	normalized := map[string]any{}
	if rawDigest := fields["expected_bytes_sha256"]; rawDigest != nil {
		var digest string
		if json.Unmarshal(rawDigest, &digest) != nil || !shaPattern.MatchString(digest) {
			return nil, expectations{}, sec.ErrInvalid
		}
		out.digest = digest
		normalized["expected_bytes_sha256"] = digest
	}
	excerptSupplied, refSupplied := fields["expected_excerpt"] != nil, fields["expected_excerpt_ref"] != nil
	if excerptSupplied && refSupplied {
		return nil, expectations{}, sec.ErrInvalid
	}
	// S28: null is not a value for any expectation field.
	for _, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, expectations{}, sec.ErrInvalid
		}
	}
	// S24: inline excerpts are UTF-8 text; binary targets use the digest.
	if raw := fields["expected_excerpt"]; raw != nil {
		var probe json.RawMessage
		if json.Unmarshal(raw, &probe) == nil && !utf8.Valid(raw) {
			return nil, expectations{}, sec.ErrInvalid
		}
	}
	switch {
	case excerptSupplied:
		var excerpt string
		if json.Unmarshal(fields["expected_excerpt"], &excerpt) != nil || int64(len(excerpt)) > int64(MaxInlineExcerpt) {
			return nil, expectations{}, sec.ErrInvalid
		}
		out.inline = []byte(excerpt)
		sum := sha256.Sum256(out.inline)
		out.sha = hex.EncodeToString(sum[:])
		out.length = int64(len(out.inline))
		normalized["expectation_sha256"], normalized["expectation_length"] = out.sha, out.length
	case refSupplied:
		var reference map[string]json.RawMessage
		if json.Unmarshal(fields["expected_excerpt_ref"], &reference) != nil || len(reference) != 1 {
			return nil, expectations{}, sec.ErrInvalid
		}
		var ref struct {
			UploadID string `json:"upload_id"`
		}
		if json.Unmarshal(reference["upload_id"], &ref.UploadID) != nil || !uuidPattern.MatchString(ref.UploadID) {
			return nil, expectations{}, sec.ErrInvalid
		}
		upload, uploadErr := tx.Upload(ref.UploadID)
		// S22/G8: another principal's workflow upload is hidden with 404
		// before any other check — except for the owner, who under current
		// owner authority may use another principal's upload when its target
		// binding matches this operation exactly (the import/contexts
		// owner-exception pattern).
		if uploadErr == nil && upload.PrincipalID != principalID && !owner {
			return nil, expectations{}, domain.ErrNotFound
		}
		sealed := uploadErr == nil && upload.Purpose == "body" && upload.State == "sealed" && upload.ObjectHash != ""
		if uploadErr != nil && !errors.Is(uploadErr, domain.ErrNotFound) {
			return nil, expectations{}, uploadErr
		}
		if sealed {
			var bound struct {
				Action     string `json:"action"`
				Operation  string `json:"operation_id"`
				VersionID  string `json:"version_id"`
				EvidenceID string `json:"evidence_id"`
			}
			if json.Unmarshal(upload.Target, &bound) != nil {
				return nil, expectations{}, sec.ErrInvalid
			}
			// G8: an owner adopting another principal's upload reaches
			// this point only through the ownership check above, and only
			// this exact binding lets it proceed.
			if bound.Action != "evidence_verification" || bound.Operation != key || bound.VersionID != versionID || bound.EvidenceID != evidenceID {
				return nil, expectations{}, sec.ErrInvalid
			}
			out.sha = upload.ObjectHash
			out.length, _ = strconv.ParseInt(upload.Checkpoint.AcceptedBytes, 10, 64)
		} else {
			// SN7/C6/S3 replay: the upload is unreadable, so prove the
			// supplied reference is the original one recorded on this
			// operation's intent, and the canonical target matches; then
			// restore only the stored expectation hash/length so the
			// receipt lookup replays whatever was recorded — completed,
			// failed or pending — while the remaining incoming semantic
			// inputs still compare normally.
			intent, intentErr := tx.CheckIntentByOperation(principalID, key)
			if intentErr != nil || intent.ExpectationUpload != ref.UploadID {
				if intentErr != nil && !errors.Is(intentErr, domain.ErrNotFound) {
					return nil, expectations{}, intentErr
				}
				if uploadErr != nil {
					return nil, expectations{}, uploadErr
				}
				return nil, expectations{}, domain.ErrTransition
			}
			var canonical struct {
				VersionID  string `json:"version_id"`
				EvidenceID string `json:"evidence_id"`
			}
			if json.Unmarshal(intent.CanonicalTarget, &canonical) != nil || canonical.VersionID != versionID || canonical.EvidenceID != evidenceID {
				return nil, expectations{}, domain.ErrTransition
			}
			// Restore exactly the stored expectation hash/length (S3
			// refinement): the intent's validated body carries both for
			// completed, failed and pending intents alike, so the remaining
			// incoming semantic inputs still compare normally and a changed
			// expectation never replays as the same request.
			var stored struct {
				SHA    string `json:"expectation_sha256"`
				Length int64  `json:"expectation_length"`
			}
			if len(intent.ValidatedBody) > 0 && json.Unmarshal(intent.ValidatedBody, &stored) == nil && stored.SHA != "" {
				out.sha = stored.SHA
				out.length = stored.Length
			} else if verification, exists, verr := tx.VerificationByOperation(principalID, key); verr == nil && exists && verification.ExpectedExcerptRef != "" {
				out.sha = verification.ExpectedExcerptRef
				out.length = int64(len(verification.ExpectedExcerpt))
				if object, oerr := tx.Object(out.sha); oerr == nil {
					out.length = object.Size
				}
			} else if verr != nil {
				return nil, expectations{}, verr
			} else if uploadErr != nil {
				return nil, expectations{}, uploadErr
			} else {
				return nil, expectations{}, domain.ErrTransition
			}
		}
		out.ref = ref.UploadID
		normalized["expectation_sha256"], normalized["expectation_length"] = out.sha, out.length
	}
	return encoded(normalized), out, nil
}

// loadEvidence resolves the immutable authored evidence, its source identity,
// the §7.5 project mapping and the retained-asset entry. Linkage failures are
// recorded for the unavailable outcome (SN1); a missing asset entry is the
// §3.1 3a fact while real storage errors propagate (C14).
func (s *Service) loadEvidence(tx app.WriteTx, el Eligibility, evidenceID string) (verifiedEvidence, error) {
	evidence, err := tx.QueryEvidence(el.Version.ID, evidenceID)
	if err != nil {
		return verifiedEvidence{}, err
	}
	sourceRow, err := tx.QuerySource(el.Version.ID, evidence.SourceID)
	if err != nil {
		return verifiedEvidence{}, err
	}
	ve := verifiedEvidence{evidence: evidence, source: sourceRow}
	if json.Unmarshal(sourceRow.Metadata, &ve.identity) != nil {
		return verifiedEvidence{}, sec.ErrInvalid
	}
	var evidenceMeta struct {
		Path      string `json:"path"`
		LineStart int64  `json:"line_start"`
		LineEnd   int64  `json:"line_end"`
	}
	if json.Unmarshal(evidence.Metadata, &evidenceMeta) != nil || evidenceMeta.Path == "" {
		return verifiedEvidence{}, sec.ErrInvalid
	}
	ve.location = locationBody{Path: evidenceMeta.Path, LineStart: evidenceMeta.LineStart, LineEnd: evidenceMeta.LineEnd}
	mappings, err := Mappings(tx, el.ProjectID)
	if err != nil {
		return verifiedEvidence{}, err
	}
	switch ve.identity.Kind {
	case "git":
		mapping, err := LinkGit(mappings, ve.identity.RepoID)
		if err != nil {
			if errors.Is(err, ErrMappingAmbiguous) {
				ve.linkageFailure = "mapping_ambiguous"
				return ve, nil
			}
			ve.linkageFailure = "source_not_registered"
			return ve, nil
		}
		ve.mapping = mapping
		ve.mappingSourceID = mapping.SourceID
	case "snapshot":
	default:
		return verifiedEvidence{}, sec.ErrInvalid
	}
	if assetPath := ve.identity.AssetPath; assetPath != "" {
		entry, err := tx.QueryEntry(el.Version.ContentSetID, assetPath)
		switch {
		case err == nil:
			ve.entry, ve.retained = entry, true
		case errors.Is(err, domain.ErrNotFound):
			// §3.1 state 3a: defined but absent (C14).
		default:
			return verifiedEvidence{}, err
		}
	}
	return ve, nil
}

// readTarget is stage 2: read the exact bounded bytes outside the writer
// lock. Evidence whose source row provides retained bytes verifies from them
// first (C9); git-kind evidence falls back to the exact-revision reader when
// the retained object is unusable, snapshot-kind records the typed fact.
func (s *Service) readTarget(ctx context.Context, res reservation) (checkOutcome, error) {
	out := checkOutcome{verificationID: mustID(), outcome: "captured"}
	ve := res.evidence
	fail := func(reason string) (checkOutcome, error) {
		out.outcome, out.reason = "unavailable", reason
		return out, nil
	}
	var expectCap int64
	if len(res.expectations.inline) > 0 {
		expectCap = int64(len(res.expectations.inline))
	}
	if ve.linkageFailure != "" {
		return fail(ve.linkageFailure)
	}
	gitRead := func() (checkOutcome, error) {
		if err := s.Reader.ResolveRevision(ctx, ve.mapping.RootPath, ve.identity.ExactRevision); err != nil {
			switch {
			case errors.Is(err, ErrInvalidRoot):
				return fail("missing_source")
			case errors.Is(err, ErrObjectMissing):
				return fail("revision_unavailable")
			default:
				// A git failure or an I/O error on the checkout is no
				// source fact: it settles 503 (S12, G14, W3e).
				return out, err
			}
		}
		stream, err := s.Reader.ReadBlob(ctx, ve.mapping.RootPath, ve.identity.ExactRevision, ve.location.Path)
		if err != nil {
			switch {
			case errors.Is(err, ErrInvalidLocation):
				return fail("location_invalid")
			case errors.Is(err, ErrObjectMissing):
				return fail("objects_missing")
			default:
				return out, err
			}
		}
		result, err := SelectBounded(stream, ve.location.LineStart, ve.location.LineEnd, expectCap)
		closeErr := stream.Close()
		if err != nil {
			if errors.Is(err, ErrLineRangeOutOfBounds) {
				return fail("line_range_out_of_bounds")
			}
			return out, err
		}
		if closeErr != nil {
			return out, closeErr
		}
		out.readID, out.targetSetID, out.bytesSHA, out.bytesOrigin = mustID(), res.targetSetID, result.SHA, "approved_source"
		return s.compare(res, out, result), nil
	}
	if !ve.retained {
		if ve.identity.Kind == "git" {
			return gitRead()
		}
		return fail("missing_retained_bytes")
	}
	var result BoundedResult
	err := s.Bytes.Read(ctx, domain.Object{Hash: ve.entry.Hash, Size: ve.entry.Size, State: "ready"}, domain.Pin{Kind: domain.CheckIntentPinOwner, OwnerID: res.intentID}, func(reader io.ReadSeeker) error {
		var err error
		result, err = SelectBounded(reader, ve.location.LineStart, ve.location.LineEnd, expectCap)
		return err
	})
	if err != nil {
		// S12: an authored range past the retained bytes is its own
		// unavailable fact for every kind.
		if errors.Is(err, ErrLineRangeOutOfBounds) {
			return fail("line_range_out_of_bounds")
		}
		if ve.identity.Kind == "git" {
			// S12 (refinement): the git fallback is limited to git-kind
			// evidence whose retained bytes are clearly absent or corrupt.
			if errors.Is(err, domain.ErrContentIntegrity) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDeleting) || errors.Is(err, fs.ErrNotExist) {
				return gitRead()
			}
			return out, err
		}
		if errors.Is(err, domain.ErrContentIntegrity) {
			return fail("retained_bytes_corrupt")
		}
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDeleting) || errors.Is(err, fs.ErrNotExist) {
			return fail("missing_retained_bytes")
		}
		return out, err
	}
	out.readID, out.targetSetID, out.bytesSHA, out.bytesOrigin = mustID(), res.targetSetID, result.SHA, "retained_snapshot"
	return s.compare(res, out, result), nil
}

// compare applies the supplied expectations to the captured bounded bytes;
// every supplied expectation must match.
func (s *Service) compare(res reservation, out checkOutcome, result BoundedResult) checkOutcome {
	if res.digestExpected == "" && res.expectations.sha == "" {
		out.outcome = "captured"
		return out
	}
	if res.expectations.sha != "" {
		if res.expectations.ref != "" {
			// A referenced expectation must equal the entire bounded bytes:
			// digest equality is the exact byte comparison.
			if result.SHA != res.expectations.sha {
				out.outcome, out.reason = "mismatch", "excerpt_mismatch"
				return out
			}
		} else if !ExcerptMatches(result, res.expectations.inline) {
			out.outcome, out.reason = "mismatch", "excerpt_mismatch"
			return out
		}
	}
	if res.digestExpected != "" && result.SHA != res.digestExpected {
		out.outcome, out.reason = "mismatch", "bytes_mismatch"
		return out
	}
	out.outcome = "matched"
	return out
}

// commitVerification is stage 3: recheck current authority (session,
// eligibility and the intent's captured config/permission generations, SN2 +
// C8), then persist the durable check records and complete the receipt
// atomically.
func (s *Service) commitVerification(ctx context.Context, token, key string, res reservation, outcome checkOutcome) (response domain.Response, err error) {
	checkedAt := s.now()
	verifiedAt := ""
	if outcome.outcome == "captured" || outcome.outcome == "matched" {
		verifiedAt = checkedAt
	}
	policySettled := false
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if err := s.fault("before_commit"); err != nil {
			return err
		}
		actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		if actor.PrincipalID != res.principalID {
			return domain.ErrDenied
		}
		el, e := Check(tx, actor, res.versionID)
		if e != nil {
			return e
		}
		if !el.MayReadSource() {
			return domain.ErrDenied
		}
		state, e := tx.Scope(el.ProjectID)
		if e != nil {
			return e
		}
		policyReason := ""
		if state.Project.PermissionsRevision != res.permissions {
			// An access-affecting change intervened: fail typed inside this
			// same transaction (SN2): receipt failed, intent failed, pins
			// released and no verification row, all atomically.
			policyReason = "permissions_changed"
		} else if state.ConfigRevision != res.configRevision {
			// SN2/C8: any committed configuration change between reservation
			// and commit invalidates the captured policy basis.
			policyReason = "config_changed"
		}
		if policyReason != "" {
			failed := errorEnvelope(412, "PRECONDITION_FAILED", policyReason)
			response = failed
			if e := s.failWithin(tx, res, 412, "PRECONDITION_FAILED", policyReason, checkedAt, failed); e != nil {
				// G2: failWithin itself failed — the settlement rolled back;
				// take the normal error path instead of returning 412.
				return e
			}
			policySettled = true
			return nil
		}
		if outcome.readID != "" {
			if e := tx.PutCapturedRead(domain.CapturedRead{ID: outcome.readID, TargetSetID: res.targetSetID, Basis: "project_source", SourceJSON: res.evidence.source.Metadata, LocationJSON: encoded(res.evidence.location), BytesSHA: outcome.bytesSHA, ConfigRevision: res.configRevision, PermissionsRevision: res.permissions, CreatedAt: checkedAt}); e != nil {
				return e
			}
		}
		verification := domain.CitationVerification{ID: outcome.verificationID, PrincipalID: res.principalID, VersionID: res.versionID, EvidenceID: res.evidenceID, ReadID: outcome.readID, TargetSetID: outcome.targetSetID, SourceJSON: res.evidence.source.Metadata, LocationJSON: encoded(res.evidence.location), BytesSHA: outcome.bytesSHA, BytesOrigin: outcome.bytesOrigin, Outcome: outcome.outcome, ExpectedBytesSHA: res.digestExpected, ExpectedExcerpt: res.expectations.inline, ConfigRevision: res.configRevision, PermissionsRevision: res.permissions, CheckedAt: checkedAt, VerifiedAt: verifiedAt, Reason: outcome.reason, OperationID: key}
		if res.expectations.ref != "" {
			// D3: adopt from the expectation hash reserved at staging and
			// the intent-owned pin — never from mutable upload state — so an
			// upload abandoned during the read neither skips adoption nor
			// leaks the pin. The record keeps the expectation reference.
			verification.ExpectedExcerptRef = res.expectations.sha
			if res.pinID != "" {
				if e := tx.Adopt(domain.Reference{Kind: "verification", ID: outcome.verificationID, Hash: res.expectations.sha}, res.pinID); e != nil {
					return e
				}
			} else if e := tx.Adopt(domain.Reference{Kind: "verification", ID: outcome.verificationID, Hash: res.expectations.sha}, ""); e != nil {
				return e
			}
		}
		if e := tx.PutVerification(verification); e != nil {
			return e
		}
		if e := tx.ResolveCheckIntent(res.intentID, "completed", "", checkedAt); e != nil {
			return e
		}
		// S15: the success commit releases every intent-owned pin, not only
		// the adopted expectation pin.
		if e := tx.ReleaseCheckIntentPins(res.intentID); e != nil {
			return e
		}
		response = domain.Response{Status: 201, Body: encoded(verificationView(verification, key))}
		return tx.CompleteOperation(domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, response)
	})
	if err == nil && policySettled {
		// S2/G2: the typed policy failure settled atomically and committed;
		// a rolled-back settlement (failWithin or commit failure) takes the
		// normal error path above.
		return response, nil
	}
	if err != nil {
		// D4: settle even when the detached bound expired inside the
		// completion transaction — a separate settlement context keeps the
		// receipt, intent and pins terminal. A bound expiry is a recovering
		// condition, never an authority failure.
		if errors.Is(err, errFaultInjected) {
			response, err := s.settleFailed(context.WithoutCancel(ctx), res, 503, "SERVICE_RECOVERING", "injected_before_commit")
			return response, err
		}
		if ctx.Err() != nil {
			response, err := s.settleFailed(context.WithoutCancel(ctx), res, 503, "SERVICE_RECOVERING", "check_bound_exceeded")
			return response, err
		}
		// S1: settle by the actual failure class. Session errors, lost
		// eligibility and denied actions become typed terminal receipts;
		// anything else is never a permanent authority failure.
		if status, code := settleClass(err); code != "" {
			response, err := s.settleFailed(context.WithoutCancel(ctx), res, status, code, settleReason(code))
			return response, err
		}
		settled, settleErr := s.settleFailedQuiet(context.WithoutCancel(ctx), res, err)
		if settleErr != nil {
			// Settlement itself failed: surface the original error and leave
			// the intent for the restart sweep.
			return domain.Response{}, errors.Join(err, settleErr)
		}
		// G13: return the settled response so the first response equals the
		// replay byte for byte.
		return settled, nil
	}
	return response, nil
}

// errExpectationNotUTF8 marks referenced expectation bytes that are not valid
// UTF-8 (G10).
var errExpectationNotUTF8 = errors.New("expected excerpt reference is not valid UTF-8")

// validateExpectationUTF8 streams the referenced expectation bytes under the
// intent pin and proves they are UTF-8 text with no new size limit (G10).
func (s *Service) validateExpectationUTF8(ctx context.Context, res reservation) error {
	if s.Bytes == nil {
		return nil
	}
	return s.Bytes.Read(ctx, domain.Object{Hash: res.expectations.sha, Size: res.expectationByteSize, State: "ready"}, domain.Pin{Kind: domain.CheckIntentPinOwner, OwnerID: res.intentID}, func(reader io.ReadSeeker) error {
		var carry []byte
		buffer := make([]byte, 64*1024)
		for {
			n, err := reader.Read(buffer)
			window := append(append([]byte{}, carry...), buffer[:n]...)
			index := 0
			for index < len(window) {
				r, size := utf8.DecodeRune(window[index:])
				if r == utf8.RuneError && size <= 1 {
					if !utf8.FullRune(window[index:]) {
						// A trailing incomplete rune carries to the next
						// round; more than UTFMax bytes can never complete.
						break
					}
					return errExpectationNotUTF8
				}
				index += size
			}
			carry = append(carry[:0], window[index:]...)
			if len(carry) >= utf8.UTFMax {
				return errExpectationNotUTF8
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					if len(carry) > 0 {
						return errExpectationNotUTF8
					}
					return nil
				}
				return err
			}
		}
	})
}

// settleClass maps a stage-3 failure to its registry class (S1): session
// errors ⇒ 401; a lost project/version ⇒ 404 (non-disclosing); a denied
// action ⇒ 403. Empty means infrastructure — never a permanent authority
// failure.
func settleClass(err error) (int, string) {
	switch {
	case errors.Is(err, sec.ErrRequired):
		return 401, "AUTH_REQUIRED"
	case errors.Is(err, sec.ErrExpired):
		return 401, "SESSION_EXPIRED"
	case errors.Is(err, sec.ErrRevoked):
		return 401, "SESSION_REVOKED"
	case errors.Is(err, domain.ErrNotFound):
		return 404, "NOT_FOUND"
	case errors.Is(err, domain.ErrDenied):
		return 403, "FORBIDDEN"
	}
	return 0, ""
}

func settleReason(code string) string {
	switch code {
	case "AUTH_REQUIRED", "SESSION_EXPIRED", "SESSION_REVOKED":
		return "session_invalidated_mid_check"
	case "NOT_FOUND":
		return "target_no_longer_visible"
	default:
		return "authority_recheck_failed"
	}
}

// errorEnvelope is the registry's top-level error shape (C11).
func errorEnvelope(status int, code, reason string) domain.Response {
	return domain.Response{Status: status, Body: encoded(map[string]any{"error": map[string]any{"code": code, "message": "source check failed: " + reason, "retryable": false, "details": map[string]string{"reason": reason}}, "request_id": requestStamp()})}
}

func requestStamp() string {
	id, err := service.NewID()
	if err != nil {
		return "unavailable"
	}
	return id[:16]
}

// errFaultInjected marks test-injected boundaries so settlement picks the
// recovering class instead of an authority failure.
var errFaultInjected = errors.New("fault injected")

func (s *Service) fault(stage string) error {
	if s.Fault == nil {
		return nil
	}
	if err := s.Fault(stage); err != nil {
		return errors.Join(errFaultInjected, err)
	}
	return nil
}

// failWithin settles a failed check inside the caller's transaction.
func (s *Service) failWithin(tx app.WriteTx, res reservation, status int, code, reason, now string, failed domain.Response) error {
	if err := tx.ReleaseCheckIntentPins(res.intentID); err != nil {
		return err
	}
	if err := tx.ResolveCheckIntent(res.intentID, "failed", reason, now); err != nil {
		return err
	}
	return tx.CompleteOperation(domain.OperationKey{PrincipalID: res.principalID, OperationID: res.operation}, failed)
}

// settleFailedQuiet settles an infrastructure stage-3 failure as a typed 503
// SERVICE_RECOVERING receipt (S1): a storage error must never become a
// permanent authority failure. The reason stays non-disclosing.
func (s *Service) settleFailedQuiet(ctx context.Context, res reservation, cause error) (domain.Response, error) {
	return s.settleFailed(ctx, res, 503, "SERVICE_RECOVERING", "stage_failure")
}

// settleFailed settles a failed check in its own writer transaction after a
// rolled-back or infrastructure-failed stage (SN2/SN6): the receipt completes
// as failed, the intent resolves and intent-owned pins are released. The
// settled failure response reaches the caller like any durable outcome; an
// unsettleable receipt surfaces its cause for reconciliation.
func (s *Service) settleFailed(ctx context.Context, res reservation, status int, code, reason string) (domain.Response, error) {
	failed := errorEnvelope(status, code, reason)
	if err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		return s.failWithin(tx, res, status, code, reason, s.now(), failed)
	}); err != nil {
		return domain.Response{}, err
	}
	return failed, nil
}

func verificationView(v domain.CitationVerification, key string) map[string]any {
	verifiedAt := any(nil)
	if v.VerifiedAt != "" {
		verifiedAt = v.VerifiedAt
	}
	var targetSet, bytesSHA, reason any
	if v.TargetSetID != "" {
		targetSet = v.TargetSetID
	}
	if v.BytesSHA != "" {
		bytesSHA = v.BytesSHA
	}
	if v.Reason != "" {
		reason = v.Reason
	}
	origin := any(nil)
	if v.BytesOrigin != "" {
		origin = v.BytesOrigin
	}
	return map[string]any{"operation_id": key, "verification_id": v.ID, "version_id": v.VersionID, "evidence_id": v.EvidenceID, "outcome": v.Outcome, "target_set_id": targetSet, "source": json.RawMessage(v.SourceJSON), "location": json.RawMessage(v.LocationJSON), "bytes_sha256": bytesSHA, "bytes_origin": origin, "checked_at": v.CheckedAt, "verified_at": verifiedAt, "reason": reason}
}

// ErrRecoveryContradiction marks an open check intent whose receipt is not
// normal terminal recovery — reconciling, missing, or contradicting the
// result rows. Recover reports it naming the intent instead of resolving it,
// and the daemon does not start over it (W2c S18, G6).
var ErrRecoveryContradiction = errors.New("check intent recovery contradiction")

// Recover resolves open check intents deterministically after a crash: a
// committed result completes its receipt; otherwise the bounded local check
// never committed and had no external effect, so the receipt fails with the
// recorded reason (S14: 503 SERVICE_RECOVERING, never OPERATION_CONFLICT).
// Terminal receipts stay untouched; every intent row is resolved or the
// sweep surfaces the real database error (S18). No check_intent pin
// survives a restart (S15).
func (s *Service) Recover(ctx context.Context) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		intents, err := tx.OpenCheckIntents()
		if err != nil {
			return err
		}
		for _, intent := range intents {
			if err := tx.ReleaseCheckIntentPins(intent.ID); err != nil {
				return err
			}
			if intent.Kind == "observation" {
				if err := s.recoverObservationIntent(tx, intent); err != nil {
					return err
				}
				continue
			}
			verification, exists, err := tx.VerificationByOperation(intent.PrincipalID, intent.OperationID)
			if err != nil {
				return err
			}
			receipt, receiptErr := tx.Receipt(domain.OperationKey{PrincipalID: intent.PrincipalID, OperationID: intent.OperationID})
			if receiptErr != nil && !errors.Is(receiptErr, domain.ErrNotFound) {
				return receiptErr
			}
			terminal := receiptErr == nil && (receipt.State == "completed" || receipt.State == "failed")
			if terminal {
				// G6: a terminal receipt is preserved. Check it against the
				// result rows before resolving the intent: a completed
				// receipt needs its verification row; a failed receipt must
				// not have one. Anything else is not normal terminal
				// recovery and names the intent instead of silently
				// resolving or overwriting it.
				contradiction := ""
				switch {
				case receipt.State == "completed" && (!exists || receipt.Response == nil || receipt.Response.Status != 201):
					contradiction = "completed receipt without its verification result"
				case receipt.State == "failed" && exists:
					contradiction = "failed receipt with a verification row"
				}
				if contradiction != "" {
					return fmt.Errorf("%w: check intent %s: %s", ErrRecoveryContradiction, intent.ID, contradiction)
				}
				if err := tx.ResolveCheckIntent(intent.ID, receipt.State, "", s.now()); err != nil {
					if errors.Is(err, domain.ErrTransition) {
						continue
					}
					return err
				}
				continue
			}
			if receiptErr == nil && receipt.State == "reconciling" {
				// G6: recovery uncertainty is not silently resolved here.
				return fmt.Errorf("%w: check intent %s: reconciling receipt", ErrRecoveryContradiction, intent.ID)
			}
			if errors.Is(receiptErr, domain.ErrNotFound) {
				// G6: an open intent without its receipt is not normal
				// terminal recovery; name it instead of inventing a result.
				return fmt.Errorf("%w: check intent %s: no operation receipt", ErrRecoveryContradiction, intent.ID)
			}
			// The receipt is still accepted: settle it deterministically.
			if exists {
				if err := tx.ResolveCheckIntent(intent.ID, "completed", "", s.now()); err != nil {
					if errors.Is(err, domain.ErrTransition) {
						continue
					}
					return err
				}
				response := domain.Response{Status: 201, Body: encoded(verificationView(verification, intent.OperationID))}
				if err := tx.CompleteOperation(domain.OperationKey{PrincipalID: intent.PrincipalID, OperationID: intent.OperationID}, response); err != nil {
					return err
				}
				continue
			}
			if err := tx.ResolveCheckIntent(intent.ID, "failed", "interrupted_before_commit", s.now()); err != nil {
				if errors.Is(err, domain.ErrTransition) {
					continue
				}
				return err
			}
			failed := errorEnvelope(503, "SERVICE_RECOVERING", "interrupted_before_commit")
			if err := tx.CompleteOperation(domain.OperationKey{PrincipalID: intent.PrincipalID, OperationID: intent.OperationID}, failed); err != nil {
				return err
			}
		}
		// S15: no check survives a restart — release any pin whose intent
		// is no longer open (crash between adoption bookkeeping stages).
		return tx.CleanupCheckIntentPins()
	})
}

// recoverObservationIntent resolves one open observation intent (S14): the
// per-source rows are the durable result — a committed run recompletes its
// receipt with the recorded 201; a run without rows never committed and had
// no external effect, so the receipt fails with 503 SERVICE_RECOVERING
// interrupted_before_commit. Contradictions name the intent (G6).
func (s *Service) recoverObservationIntent(tx app.WriteTx, intent domain.CheckIntent) error {
	rows, err := tx.ObservationsByOperation(intent.PrincipalID, intent.OperationID)
	if err != nil {
		return err
	}
	run, runExists, err := tx.ObservationRunForOperation(intent.PrincipalID, intent.OperationID)
	if err != nil {
		return err
	}
	receipt, receiptErr := tx.Receipt(domain.OperationKey{PrincipalID: intent.PrincipalID, OperationID: intent.OperationID})
	if receiptErr != nil && !errors.Is(receiptErr, domain.ErrNotFound) {
		return receiptErr
	}
	terminal := receiptErr == nil && (receipt.State == "completed" || receipt.State == "failed")
	if terminal {
		contradiction := ""
		switch {
		case receipt.State == "completed" && (!runExists || len(rows) == 0 || receipt.Response == nil || receipt.Response.Status != 201):
			contradiction = "completed receipt without its observation rows"
		case receipt.State == "failed" && len(rows) > 0:
			contradiction = "failed receipt with observation rows"
		}
		if contradiction != "" {
			return fmt.Errorf("%w: check intent %s: %s", ErrRecoveryContradiction, intent.ID, contradiction)
		}
		if err := tx.ResolveCheckIntent(intent.ID, receipt.State, "", s.now()); err != nil {
			if errors.Is(err, domain.ErrTransition) {
				return nil
			}
			return err
		}
		return nil
	}
	if receiptErr == nil && receipt.State == "reconciling" {
		return fmt.Errorf("%w: check intent %s: reconciling receipt", ErrRecoveryContradiction, intent.ID)
	}
	if errors.Is(receiptErr, domain.ErrNotFound) {
		return fmt.Errorf("%w: check intent %s: no operation receipt", ErrRecoveryContradiction, intent.ID)
	}
	if len(rows) > 0 && runExists {
		// The work committed; recompose the recorded 201 from the rows.
		triggers := map[string]bool{}
		if run.TargetSetID != "" {
			rowsT, err := tx.TriggersByTargetSet(run.VersionID, run.TargetSetID)
			if err != nil {
				return err
			}
			for _, trigger := range rowsT {
				if trigger.RunID == run.ID {
					triggers[trigger.SourceID] = true
				}
			}
		}
		if err := tx.ResolveCheckIntent(intent.ID, "completed", "", s.now()); err != nil {
			if errors.Is(err, domain.ErrTransition) {
				return nil
			}
			return err
		}
		// W3b L12: the same view function as the commit, so the recomposed
		// 201 equals the original shape.
		coverageSetID := ""
		if set, err := tx.CoverageSetByRun(run.ID); err == nil {
			coverageSetID = set.ID
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		response := domain.Response{Status: 201, Body: encoded(observationView(intent.OperationID, run, coverageSetID, rows, triggers))}
		return tx.CompleteOperation(domain.OperationKey{PrincipalID: intent.PrincipalID, OperationID: intent.OperationID}, response)
	}
	if err := tx.ResolveCheckIntent(intent.ID, "failed", "interrupted_before_commit", s.now()); err != nil {
		if errors.Is(err, domain.ErrTransition) {
			return nil
		}
		return err
	}
	failed := errorEnvelope(503, "SERVICE_RECOVERING", "interrupted_before_commit")
	return tx.CompleteOperation(domain.OperationKey{PrincipalID: intent.PrincipalID, OperationID: intent.OperationID}, failed)
}
