package auth

import (
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/url"
	"strings"
)

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func RestrictScope(captured, current domain.CredentialScope) domain.CredentialScope {
	return domain.RestrictCredentialScope(captured, current)
}
func (s *Service) scoped(tx app.WriteTx, token string, state domain.AuthState) (domain.Session, error) {
	c, e := tx.ScopedCredentialByHash(sec.Verifier("workflow", token))
	if errors.Is(e, domain.ErrNotFound) {
		return domain.Session{}, sec.ErrRequired
	}
	if e != nil {
		return domain.Session{}, e
	}
	p, e := tx.WorkflowPrincipal(c.PrincipalID)
	if e != nil {
		return domain.Session{}, e
	}
	if c.Revoked || !p.Active || c.AuthEpoch != state.AuthEpoch {
		return domain.Session{}, sec.ErrRevoked
	}
	if c.ExpiresAt != nil && !s.now().Before(*c.ExpiresAt) {
		return domain.Session{}, sec.ErrExpired
	}
	for id, revision := range c.Permissions {
		v, e := tx.Scope(id)
		if e != nil {
			return domain.Session{}, e
		}
		if v.Project.PermissionsRevision != revision {
			return domain.Session{}, domain.ErrDenied
		}
	}
	r := domain.Session{ID: c.ID, PrincipalID: c.PrincipalID, LibraryID: state.LibraryID, CreatedAt: c.CreatedAt, LastSeenAt: c.CreatedAt, ExpiresAt: c.ExpiresAt, Scope: RestrictScope(c.Scope, p.Scope), MutationProjectID: c.MutationProjectID, AuthEpoch: c.AuthEpoch, State: "active", Revision: c.Revision}
	return sec.Projection(r, nil)
}

// AuthorizeUploadScope checks the captured action and exact resource anchors.
// Current target lifecycle is checked separately by storage.AuthorizeTarget.
func AuthorizeUploadScope(scope domain.CredentialScope, purpose string, raw []byte) error {
	if scope.Kind == "owner" {
		return nil
	}
	var target struct {
		ProjectID string  `json:"project_id"`
		DraftID   *string `json:"draft_id"`
		VersionID string  `json:"version_id"`
		Action    string  `json:"action"`
	}
	if json.Unmarshal(raw, &target) != nil {
		return sec.ErrInvalid
	}
	action := ""
	switch purpose {
	case "result":
		if target.DraftID != nil {
			action = "draft.write"
		} else {
			action = "import.publish"
		}
	case "metadata":
		switch target.Action {
		case "draft_replace":
			action = "draft.write"
		case "import":
			action = "import.publish"
		}
	case "context":
		action = "context.write"
	case "body":
		switch target.Action {
		case "generation":
			action = "generation.request"
		case "evidence_verification":
			// The sealed expectation transfer for the exact verification
			// operation requires both byte-affecting actions; the shared
			// anchor checks below still apply (TASK-004-01).
			if !contains(scope.Actions, "version.read") || !contains(scope.Actions, "source.read") {
				return domain.ErrDenied
			}
			action = "version.read"
		}
	}
	if action == "" || !contains(scope.Actions, action) {
		return domain.ErrDenied
	}
	if target.DraftID != nil {
		if !contains(scope.DraftIDs, *target.DraftID) {
			return domain.ErrDenied
		}
	}
	if target.ProjectID != "" && !contains(scope.ProjectIDs, target.ProjectID) {
		return domain.ErrDenied
	}
	if target.VersionID != "" && !contains(scope.VersionIDs, target.VersionID) {
		return domain.ErrDenied
	}
	return nil
}
func authorizeScope(tx app.WriteTx, record domain.Session, r domain.Request) error {
	if handled, e := authorizeManagedCapture(tx, record, r); handled {
		return e
	}
	if record.Scope.Kind == "owner" {
		return nil
	}
	path := strings.SplitN(r.Target, "?", 2)[0]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
		return domain.ErrDenied
	}
	switch parts[2] {
	case "drafts":
		read := r.Method == "GET" || r.Method == "HEAD"
		if len(parts) == 3 && read {
			if contains(record.Scope.Actions, "draft.write") || contains(record.Scope.Actions, "draft.publish") {
				return nil
			}
			return domain.ErrDenied
		}
		if len(parts) < 4 || len(parts) > 5 {
			return domain.ErrDenied
		}
		draft, e := tx.Draft(parts[3])
		if e != nil {
			return e
		}
		if !contains(record.Scope.DraftIDs, draft.ID) || !contains(record.Scope.ProjectIDs, draft.ProjectID) {
			return domain.ErrNotFound
		}
		if len(parts) == 5 && parts[4] == "publish" && r.Method == "POST" {
			if contains(record.Scope.Actions, "draft.publish") {
				return nil
			}
		} else if len(parts) == 4 && (read || r.Method == "PATCH") {
			if contains(record.Scope.Actions, "draft.write") || read && contains(record.Scope.Actions, "draft.publish") {
				return nil
			}
		}
		return domain.ErrDenied
	case "generations":
		if r.Method != "POST" || len(parts) != 3 {
			return domain.ErrDenied
		}
		var input struct {
			Project  string  `json:"project_id"`
			Document *string `json:"document_id"`
		}
		if json.Unmarshal(r.Body, &input) != nil || input.Project == "" {
			return sec.ErrInvalid
		}
		target, _ := json.Marshal(map[string]any{"action": "generation", "project_id": input.Project, "document_id": input.Document})
		if e := authorizeUpload(tx, record.Scope, "body", target); e != nil {
			return e
		}
		return app.AuthorizeTarget(tx, target)
	case "scans":
		if r.Method != "POST" || len(parts) != 3 || !contains(record.Scope.Actions, "scan.request") {
			return domain.ErrDenied
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var input struct {
			ProjectID   string   `json:"project_id"`
			ProjectIDs  []string `json:"project_ids"`
			DocumentID  string   `json:"document_id"`
			DocumentIDs []string `json:"document_ids"`
			VersionID   string   `json:"version_id"`
		}
		if len(r.Body) > 0 && json.Unmarshal(r.Body, &input) != nil {
			return sec.ErrInvalid
		}
		projects := []string{}
		if strings.TrimSpace(input.ProjectID) != "" {
			projects = append(projects, strings.TrimSpace(input.ProjectID))
		}
		for _, pid := range input.ProjectIDs {
			if strings.TrimSpace(pid) != "" {
				projects = append(projects, strings.TrimSpace(pid))
			}
		}
		vid := strings.TrimSpace(input.VersionID)
		if vid != "" {
			_, d, e := app.CheckSourceVersion(tx, record, vid)
			if e != nil {
				return e
			}
			projects = append(projects, d.ProjectID)
		}
		docIDs := []string{}
		if strings.TrimSpace(input.DocumentID) != "" {
			docIDs = append(docIDs, strings.TrimSpace(input.DocumentID))
		}
		for _, did := range input.DocumentIDs {
			if strings.TrimSpace(did) != "" {
				docIDs = append(docIDs, strings.TrimSpace(did))
			}
		}
		for _, did := range docIDs {
			d, e := tx.Document(did)
			if e != nil {
				return e
			}
			if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
				return domain.ErrNotFound
			}
			if !contains(record.Scope.ProjectIDs, d.ProjectID) {
				return domain.ErrNotFound
			}
			projects = append(projects, d.ProjectID)
			if vid == "" {
				if d.LatestVersionID == "" {
					return domain.ErrNotFound
				}
				if _, _, e := app.CheckSourceVersion(tx, record, d.LatestVersionID); e != nil {
					return e
				}
			}
		}
		if len(projects) == 0 {
			if record.MutationProjectID != "" {
				projects = []string{record.MutationProjectID}
			} else {
				projects = append(projects, record.Scope.ProjectIDs...)
			}
		}
		for _, pid := range projects {
			if record.MutationProjectID != "" && pid != record.MutationProjectID {
				return domain.ErrDenied
			}
			if !contains(record.Scope.ProjectIDs, pid) || !projectPostWorkScanEnabled(cfg, pid) {
				return domain.ErrDenied
			}
			if _, e := tx.Scope(pid); e != nil {
				return e
			}
		}
		return nil
	case "lifecycle-plans", "trash":
		return domain.ErrNotFound
	case "annotations":
		return domain.ErrDenied
	case "search":
		if (r.Method == "GET" || r.Method == "HEAD") && len(parts) == 3 && contains(record.Scope.Actions, "version.read") {
			return nil
		}
		return domain.ErrDenied
	case "capabilities":
		if r.Method == "GET" {
			return nil
		}
	case "uploads":
		if len(parts) > 3 {
			u, e := tx.Upload(parts[3])
			if e != nil {
				return e
			}
			if u.PrincipalID != record.PrincipalID {
				return domain.ErrNotFound
			}
			return authorizeUpload(tx, record.Scope, u.Purpose, u.Target)
		}
		if r.Method == "GET" {
			return nil
		} // list adapter applies principal/target filters before pagination.
		var body struct {
			Purpose string          `json:"purpose"`
			Target  json.RawMessage `json:"target"`
		}
		if json.Unmarshal(r.Body, &body) != nil {
			return sec.ErrInvalid
		}
		return authorizeUpload(tx, record.Scope, body.Purpose, body.Target)
	case "imports", "packages":
		if contains(record.Scope.Actions, "import.publish") {
			return nil
		}
	case "contexts":
		// POST follows the context-writing workflow action with the exact
		// version/project anchors enforced here so replayed receipts cannot
		// outlive a scope loss. A record read is authorized by the record's
		// own principal ownership check in the service.
		if r.Method == "POST" && len(parts) == 3 && contains(record.Scope.Actions, "context.write") {
			if query := strings.SplitN(r.Target, "?", 2); len(query) == 2 {
				if values, e := url.ParseQuery(query[1]); e == nil {
					if version := values.Get("version_id"); version != "" {
						v, e := tx.PublishedVersion(version)
						if e != nil {
							return e
						}
						d, e := tx.Document(v.DocumentID)
						if e != nil {
							return e
						}
						if contains(record.Scope.VersionIDs, version) && contains(record.Scope.ProjectIDs, d.ProjectID) {
							return nil
						}
					}
				}
			}
			return domain.ErrNotFound
		}
		if r.Method == "GET" && len(parts) == 4 {
			return nil
		}
	case "projects":
		if r.Method == "POST" && len(parts) == 3 {
			// Project registration is owner-only (plan §3).
			return domain.ErrDenied
		}
		if r.Method == "GET" && len(parts) == 5 && parts[4] == "sources" {
			if contains(record.Scope.Actions, "project.read") && contains(record.Scope.ProjectIDs, parts[3]) {
				return nil
			}
			return domain.ErrNotFound
		}
		fallthrough
	case "documents", "versions":
		if len(parts) >= 5 && (parts[4] == "review" || parts[4] == "annotations" || parts[4] == "revisit") {
			return domain.ErrDenied
		}
		// Render-session opens follow the render-bootstrap authority family:
		// the scoped version.read action with the exact version/project
		// anchors. Renewal and close of a known session are controlled by the
		// renderer service's own current-authority checks.
		if parts[2] == "versions" && len(parts) == 5 && parts[4] == "render-sessions" && r.Method == "POST" {
			if !contains(record.Scope.Actions, "version.read") {
				return domain.ErrDenied
			}
			v, e := tx.PublishedVersion(parts[3])
			if e != nil {
				return e
			}
			d, e := tx.Document(v.DocumentID)
			if e != nil {
				return e
			}
			if contains(record.Scope.VersionIDs, v.ID) && contains(record.Scope.ProjectIDs, d.ProjectID) {
				return nil
			}
			return domain.ErrNotFound
		}
		// Imported-evidence verification follows the byte-affecting action
		// layer: version.read plus source.read with the exact version and
		// project anchors (TASK-004-01 plan §4). The canonical query form
		// and the catalog path form share this rule; slash-containing
		// evidence IDs only route through the query form.
		if parts[2] == "versions" && r.Method == "POST" && ((len(parts) == 5 && parts[4] == "source-evidence-verifications") || (len(parts) == 7 && parts[4] == "source-evidence" && parts[6] == "verifications")) {
			if !contains(record.Scope.Actions, "version.read") || !contains(record.Scope.Actions, "source.read") {
				return domain.ErrDenied
			}
			v, e := tx.PublishedVersion(parts[3])
			if e != nil {
				return e
			}
			d, e := tx.Document(v.DocumentID)
			if e != nil {
				return e
			}
			if contains(record.Scope.VersionIDs, v.ID) && contains(record.Scope.ProjectIDs, d.ProjectID) {
				return nil
			}
			return domain.ErrNotFound
		}
		// Source observations are owner-only in TASK-004-01 (plan §3/O1).
		if parts[2] == "versions" && r.Method == "POST" && len(parts) == 5 && parts[4] == "source-observations" {
			return domain.ErrDenied
		}
		// The package archive is an export action with its own scope: the
		// exact project/version anchors plus package.export authorize it,
		// while version.read alone does not and package.export alone grants
		// no ordinary version reads.
		if parts[2] == "versions" && len(parts) == 5 && parts[4] == "package" && r.Method == "GET" {
			if !contains(record.Scope.Actions, "package.export") {
				return domain.ErrDenied
			}
			v, e := tx.PublishedVersion(parts[3])
			if e != nil {
				return e
			}
			d, e := tx.Document(v.DocumentID)
			if e != nil {
				return e
			}
			if contains(record.Scope.ProjectIDs, d.ProjectID) && contains(record.Scope.VersionIDs, v.ID) {
				return nil
			}
			return domain.ErrNotFound
		}
		// Query SQL additionally binds each list to these current allowed anchors.
		if r.Method == "GET" && (contains(record.Scope.Actions, "version.read") || contains(record.Scope.Actions, "project.read")) {
			if len(parts) == 3 {
				if parts[2] == "versions" || contains(record.Scope.Actions, "project.read") {
					return nil
				}
				return domain.ErrDenied
			}
			switch parts[2] {
			case "projects":
				if contains(record.Scope.Actions, "project.read") && contains(record.Scope.ProjectIDs, parts[3]) {
					return nil
				}
			case "documents":
				if strings.HasSuffix(path, "/lifecycle-plans") {
					return domain.ErrNotFound
				}
				if len(parts) == 5 && parts[4] == "versions" && contains(record.Scope.Actions, "version.read") {
					for _, id := range record.Scope.VersionIDs {
						v, e := tx.PublishedVersion(id)
						if errors.Is(e, domain.ErrNotFound) {
							continue
						}
						if e != nil {
							return e
						}
						if v.DocumentID == parts[3] {
							d, e := tx.Document(v.DocumentID)
							if e != nil {
								return e
							}
							if contains(record.Scope.ProjectIDs, d.ProjectID) {
								return nil
							}
						}
					}
					return domain.ErrNotFound
				}
				d, e := tx.Document(parts[3])
				if e != nil {
					return e
				}
				if contains(record.Scope.Actions, "project.read") && contains(record.Scope.ProjectIDs, d.ProjectID) {
					return nil
				}
			case "versions":
				v, e := tx.PublishedVersion(parts[3])
				if e != nil {
					return e
				}
				d, e := tx.Document(v.DocumentID)
				if e != nil {
					return e
				}
				if contains(record.Scope.Actions, "version.read") && contains(record.Scope.ProjectIDs, d.ProjectID) && contains(record.Scope.VersionIDs, v.ID) {
					return nil
				}
			}
			return domain.ErrNotFound
		}
	case "jobs":
		if len(parts) > 4 && (parts[4] == "resume" || parts[4] == "findings") {
			return domain.ErrDenied
		}
		action := "job.read"
		if r.Method != "GET" && r.Method != "HEAD" {
			action = "job.cancel"
		}
		if !contains(record.Scope.Actions, action) {
			return domain.ErrDenied
		}
		if len(parts) > 3 {
			j, e := tx.Job(parts[3])
			if e != nil {
				return e
			}
			if j.Kind == "derived_index" {
				return domain.ErrNotFound
			}
			if j.Kind == "scan" {
				if e := visibleScanJobForActor(tx, record, j); e != nil {
					return e
				}
				return visibleJob(tx, j)
			}
			var owner struct {
				Principal string          `json:"principal_id"`
				Purpose   string          `json:"purpose"`
				Target    json.RawMessage `json:"target"`
			}
			if json.Unmarshal(j.Scope, &owner) != nil || owner.Principal != record.PrincipalID {
				return domain.ErrNotFound
			}
			return AuthorizeUploadScope(record.Scope, owner.Purpose, owner.Target)
		}
		return nil
	}
	return domain.ErrDenied
}

// AuthorizeAccepted checks stable principal/current binding against the immutable
// accepted scope. Credential logout/expiry does not cancel already accepted work.
func AuthorizeAccepted(tx app.WriteTx, principal, epoch, purpose string, target []byte, captured domain.CredentialScope) error {
	state, e := tx.AuthState()
	if e != nil {
		return e
	}
	if epoch != state.AuthEpoch {
		return domain.ErrDenied
	}
	if principal == state.OwnerID {
		return nil
	}
	p, e := tx.WorkflowPrincipal(principal)
	if e != nil {
		return e
	}
	if !p.Active {
		return domain.ErrDenied
	}
	return authorizeUpload(tx, RestrictScope(captured, p.Scope), purpose, target)
}

func authorizeUpload(tx app.WriteTx, scope domain.CredentialScope, purpose string, target []byte) error {
	if e := AuthorizeUploadScope(scope, purpose, target); e != nil {
		return e
	}
	if scope.Kind == "owner" {
		return nil
	}
	project, e := app.TargetProject(tx, target)
	if e != nil {
		return e
	}
	if project != "" && !contains(scope.ProjectIDs, project) {
		return domain.ErrDenied
	}
	return nil
}

func visibleScanJobForActor(tx app.WriteTx, actor domain.Session, j domain.Job) error {
	if j.Kind != "scan" {
		return nil
	}
	var scanScope struct {
		PrincipalID       string                 `json:"principal_id"`
		ProjectIDs        []string               `json:"project_ids"`
		DocumentID        string                 `json:"document_id"`
		DocumentIDs       []string               `json:"document_ids"`
		ActiveDocumentIDs []string               `json:"active_document_ids"`
		ActiveVersionIDs  []string               `json:"active_version_ids"`
		VersionID         string                 `json:"version_id"`
		CredentialScope   domain.CredentialScope `json:"credential_scope"`
	}
	if json.Unmarshal(j.Scope, &scanScope) != nil || scanScope.PrincipalID == "" {
		return domain.ErrNotFound
	}
	if actor.Scope.Kind == "owner" {
		return nil
	}
	if scanScope.PrincipalID != actor.PrincipalID {
		return domain.ErrNotFound
	}
	for _, pid := range scanScope.ProjectIDs {
		if !contains(actor.Scope.ProjectIDs, pid) {
			return domain.ErrNotFound
		}
		if _, e := tx.Scope(pid); e != nil {
			return e
		}
	}
	if scanScope.VersionID != "" {
		if !contains(actor.Scope.VersionIDs, scanScope.VersionID) {
			return domain.ErrNotFound
		}
		if e := visibleTarget(tx, "/api/v1/versions/"+scanScope.VersionID); e != nil {
			return e
		}
	}
	docIDs := []string{}
	if strings.TrimSpace(scanScope.DocumentID) != "" {
		docIDs = append(docIDs, strings.TrimSpace(scanScope.DocumentID))
	}
	for _, did := range scanScope.DocumentIDs {
		trimmed := strings.TrimSpace(did)
		if trimmed != "" && !contains(docIDs, trimmed) {
			docIDs = append(docIDs, trimmed)
		}
	}
	for _, did := range docIDs {
		d, e := tx.Document(did)
		if e != nil {
			return e
		}
		if d.Lifecycle == "trash" || d.Lifecycle == "purged" || !contains(actor.Scope.ProjectIDs, d.ProjectID) {
			return domain.ErrNotFound
		}
		if scanScope.VersionID == "" {
			if d.LatestVersionID == "" || !contains(actor.Scope.VersionIDs, d.LatestVersionID) {
				return domain.ErrNotFound
			}
		}
	}
	for _, vid := range scanScope.ActiveVersionIDs {
		if !contains(actor.Scope.VersionIDs, vid) {
			return domain.ErrNotFound
		}
		if e := visibleTarget(tx, "/api/v1/versions/"+vid); e != nil {
			return e
		}
	}
	for _, did := range scanScope.ActiveDocumentIDs {
		if e := visibleTarget(tx, "/api/v1/documents/"+did); e != nil {
			return e
		}
	}
	hasCaptured := false
	afterOrd := 0
	for {
		roster, e := tx.ScanCapturedVersions(j.ID, afterOrd, 256)
		if e != nil {
			return e
		}
		if len(roster) == 0 {
			break
		}
		hasCaptured = true
		for _, cv := range roster {
			if cv.VersionOrdinal > afterOrd {
				afterOrd = cv.VersionOrdinal
			} else {
				afterOrd++
			}
			if cv.ProjectID != "" && !contains(actor.Scope.ProjectIDs, cv.ProjectID) {
				return domain.ErrNotFound
			}
			if cv.EnumState == "purged" {
				return domain.ErrNotFound
			}
			if cv.VersionID == "" && cv.ExclusionReason != "" {
				continue
			}
			if cv.VersionID != "" {
				if !contains(actor.Scope.VersionIDs, cv.VersionID) {
					return domain.ErrNotFound
				}
				if e := visibleTarget(tx, "/api/v1/versions/"+cv.VersionID); e != nil {
					return e
				}
			}
			if cv.DocumentID != "" {
				if e := visibleTarget(tx, "/api/v1/documents/"+cv.DocumentID); e != nil {
					return e
				}
			}
		}
		if len(roster) < 256 {
			break
		}
	}
	if !hasCaptured && len(scanScope.CredentialScope.VersionIDs) > 0 {
		for _, vid := range scanScope.CredentialScope.VersionIDs {
			if !contains(actor.Scope.VersionIDs, vid) {
				return domain.ErrNotFound
			}
			if e := visibleTarget(tx, "/api/v1/versions/"+vid); e != nil {
				return e
			}
		}
	}
	return nil
}
