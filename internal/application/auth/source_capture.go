package auth

import (
	"encoding/json"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

// Receipt recovery re-applies source/target scope before returning saved input
// identity. It performs no filesystem, Git or provider read.
func authorizeManagedCapture(tx app.WriteTx, actor domain.Session, r domain.Request) (bool, error) {
	parts := strings.Split(strings.Trim(strings.SplitN(r.Target, "?", 2)[0], "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "projects" || parts[4] != "source-captures" {
		return false, nil
	}
	var input struct {
		Version    string `json:"version_id"`
		Provider   string `json:"provider_id"`
		Selections []struct {
			Source string `json:"source_id"`
		} `json:"selections"`
	}
	if json.Unmarshal(r.Body, &input) != nil {
		return true, domain.ErrNotFound
	}
	project := parts[3]
	target, _ := json.Marshal(map[string]string{"project_id": project, "version_id": input.Version})
	if e := app.AuthorizeTarget(tx, target); e != nil {
		return true, e
	}
	if actor.Scope.Kind != "owner" {
		if input.Version == "" || !contains(actor.Scope.ProjectIDs, project) || !contains(actor.Scope.VersionIDs, input.Version) {
			return true, domain.ErrNotFound
		}
		if !contains(actor.Scope.Actions, "version.read") || !contains(actor.Scope.Actions, "source.read") {
			return true, domain.ErrDenied
		}
	}
	for _, sel := range input.Selections {
		if _, e := app.ManagedSourcePolicy(tx, project, sel.Source, input.Provider); e != nil {
			return true, domain.ErrNotFound
		}
	}
	return true, nil
}
