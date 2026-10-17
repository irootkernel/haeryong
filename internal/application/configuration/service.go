package configuration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	auth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"reflect"
	"time"
)

var ErrStalePlan = errors.New("STALE_PLAN")

type Paths interface {
	Canonical(string, bool) (string, error)
}
type Service struct {
	Store            app.Store
	Auth             *auth.Service
	Paths            Paths
	Now              func() time.Time
	Restart          func()
	RuntimeEndpoint  string
	ListenerOverride bool
}

func New(store app.Store, a *auth.Service, paths Paths) *Service {
	return &Service{Store: store, Auth: a, Paths: paths, Now: time.Now}
}

type Scope struct {
	Kind      string `json:"kind"`
	ProjectID string `json:"project_id,omitempty"`
}
type PlanRequest struct {
	Scope            Scope          `json:"scope"`
	ExpectedRevision int64          `json:"expected_config_revision"`
	Changes          map[string]any `json:"changes"`
}
type CommitRequest struct {
	ExpectedRevision int64 `json:"expected_config_revision"`
	Confirmed        bool  `json:"confirmed"`
}
type View struct {
	ConfigRevision int64           `json:"config_revision"`
	ETag           string          `json:"etag"`
	Value          json.RawMessage `json:"configuration"`
}

func tag(b []byte) string {
	h := sha256.Sum256(b)
	return `"configuration-` + hex.EncodeToString(h[:]) + `"`
}
func encoded(v any) []byte { b, _ := json.Marshal(v); return b }
func (s *Service) owner(ctx context.Context, tx app.WriteTx, token string) (domain.Session, error) {
	r, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return r, e
	}
	if r.Scope.Kind != "owner" {
		return r, domain.ErrDenied
	}
	return r, nil
}
func (s *Service) Read(ctx context.Context, token string) (View, error) {
	var out View
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.owner(ctx, tx, token); e != nil {
			return e
		}
		c, e := tx.Configuration()
		if e != nil {
			return e
		}
		out = View{ConfigRevision: c.Revision, Value: c.Value}
		out.ETag = tag(encoded(out))
		return nil
	})
	return out, e
}

func (s *Service) Staging(ctx context.Context, token string) (domain.StagingSummary, error) {
	var out domain.StagingSummary
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.owner(ctx, tx, token); e != nil {
			return e
		}
		var e error
		out, e = tx.StagingSummary()
		return e
	})
	return out, err
}
func (s *Service) normalizePaths(current, value map[string]any) error {
	oldProjects := current["projects"].(map[string]any)
	for id, v := range value["projects"].(map[string]any) {
		settings := v.(map[string]any)
		old, _ := oldProjects[id].(map[string]any)
		if path, ok := settings["output_destination"].(string); ok && !reflect.DeepEqual(settings["output_destination"], old["output_destination"]) {
			if s.Paths == nil {
				return invalid("/projects/"+id+"/output_destination", "host path validation unavailable")
			}
			canonical, e := s.Paths.Canonical(path, true)
			if e != nil {
				return e
			}
			settings["output_destination"] = canonical
		}
		if sources, ok := settings["sources"].([]any); ok {
			oldSources, _ := old["sources"].([]any)
			for _, v := range sources {
				source := v.(map[string]any)
				unchanged := false
				for _, o := range oldSources {
					item := o.(map[string]any)
					if item["source_id"] == source["source_id"] && item["root_path"] == source["root_path"] {
						unchanged = true
						break
					}
				}
				if !unchanged {
					if s.Paths == nil {
						return invalid("/projects/"+id+"/sources", "host path validation unavailable")
					}
					path, e := s.Paths.Canonical(source["root_path"].(string), false)
					if e != nil {
						return e
					}
					source["root_path"] = path
				}
			}
		}
	}
	return nil
}
func access(v map[string]any, id string) any {
	p, _ := v["projects"].(map[string]any)[id].(map[string]any)
	return []any{v["source"], p["sources"], p["output_destination"]}
}
func (s *Service) Plan(ctx context.Context, token, key string, input PlanRequest) (domain.Response, error) {
	if key != "" && !uuid.MatchString(key) {
		return domain.Response{}, sec.ErrInvalid
	}

	original := encoded(input)
	record, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if record.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/configuration/plans", Body: original}
	if key != "" {
		response, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), request)
		if e != nil || found {
			return response, e
		}
	}
	var actor domain.Session
	var current domain.Configuration
	var projects []domain.QueryProject
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		actor, e = s.owner(ctx, tx, token)
		if e != nil {
			return e
		}
		current, e = tx.Configuration()
		if e != nil {
			return e
		}
		after, id := "", ""
		for {
			rows, e := tx.QueryProjects(domain.CredentialScope{Kind: "owner"}, after, id, 64)
			if e != nil {
				return e
			}
			projects = append(projects, rows...)
			if len(rows) < 64 {
				break
			}
			last := rows[len(rows)-1]
			after, id = last.UpdatedAt, last.ID
		}
		return nil
	})
	if e != nil {
		return domain.Response{}, e
	}
	if input.ExpectedRevision < 1 || input.Changes == nil {
		return domain.Response{}, sec.ErrInvalid
	}
	if input.Scope.Kind != "library" && input.Scope.Kind != "project" || input.Scope.Kind == "library" && input.Scope.ProjectID != "" || input.Scope.Kind == "project" && !uuid.MatchString(input.Scope.ProjectID) {
		return domain.Response{}, sec.ErrInvalid
	}
	if current.Revision != input.ExpectedRevision {
		return domain.Response{}, domain.ErrPrecondition
	}
	old, e := Decode(bytes.NewReader(current.Value), "json")
	if e != nil {
		return domain.Response{}, e
	}
	changes := input.Changes
	if input.Scope.Kind == "project" {
		found := false
		for _, p := range projects {
			if p.ID == input.Scope.ProjectID {
				found = true
			}
		}
		if !found {
			return domain.Response{}, domain.ErrNotFound
		}
		changes = map[string]any{"projects": map[string]any{input.Scope.ProjectID: changes}}
	}
	next := Merge(old, changes)
	if e = Validate(next); e != nil {
		return domain.Response{}, e
	}
	for id := range next["projects"].(map[string]any) {
		found := false
		for _, p := range projects {
			if p.ID == id {
				found = true
			}
		}
		if !found {
			return domain.Response{}, invalid("/projects/"+id, "existing project required")
		}
	}
	if e = s.normalizePaths(old, next); e != nil {
		return domain.Response{}, e
	}
	permissions := map[string]int64{}
	for _, p := range projects {
		if !reflect.DeepEqual(access(old, p.ID), access(next, p.ID)) {
			permissions[p.ID] = p.PermissionsRevision
		}
	}
	restart := !reflect.DeepEqual(old["network"], next["network"])
	id, e := service.NewID()
	if e != nil {
		return domain.Response{}, e
	}
	plan := domain.ConfigurationPlan{ID: id, PrincipalID: actor.PrincipalID, BaseRevision: input.ExpectedRevision, ExpiresAt: s.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339Nano), Scope: encoded(input.Scope), Changes: encoded(changes), Effective: Canonical(next), Permissions: permissions, Impact: encoded(map[string]any{"controlled_restart_required": restart, "security": "immediate", "workflow_scope": "new capture; current restrictions apply", "provider_execution": "not_ready"})}

	apply := func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		live, e := tx.Configuration()
		if e != nil {
			return domain.Response{}, e
		}
		if live.Revision != current.Revision || current.Revision != input.ExpectedRevision {
			return domain.Response{}, domain.ErrPrecondition
		}
		if e = tx.PutConfigurationPlan(plan); e != nil {
			return domain.Response{}, e
		}
		return domain.Response{Status: 200, Body: encoded(plan)}, nil
	}
	if key != "" {
		return app.Execute(ctx, s.Store, s.Auth.Authority(token), request, nil, apply)
	}
	var response domain.Response
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.owner(ctx, tx, token); e != nil {
			return e
		}
		var e error
		response, e = apply(tx, domain.Receipt{})
		return e
	})
	return response, e
}
func (s *Service) Commit(ctx context.Context, token, id, alias string, input CommitRequest) (domain.Response, error) {
	if !uuid.MatchString(id) || alias != "" && !uuid.MatchString(alias) || input.ExpectedRevision < 1 || !input.Confirmed {
		return domain.Response{}, sec.ErrInvalid
	}
	var response domain.Response
	restart := false
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.owner(ctx, tx, token)
		if e != nil {
			return e
		}
		r := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: id}, Method: "POST", Target: "/api/v1/configuration/plans/" + id + "/commit", Body: encoded(input)}
		receipt, existing, e := tx.ReserveOperation(r, nil)
		if e != nil {
			return e
		}
		if existing {
			if receipt.State != "completed" && receipt.State != "failed" {
				return domain.ErrReconciliation
			}
			response = *receipt.Response
		} else {
			plan, e := tx.ConfigurationPlan(id)
			if e != nil {
				return e
			}
			if plan.PrincipalID != actor.PrincipalID {
				return domain.ErrNotFound
			}
			expiry, e := time.Parse(time.RFC3339Nano, plan.ExpiresAt)
			if e != nil {
				return e
			}
			if !s.Now().Before(expiry) {
				return ErrStalePlan
			}
			current, e := tx.Configuration()
			if e != nil {
				return e
			}
			if plan.BaseRevision != input.ExpectedRevision || current.Revision != plan.BaseRevision {
				return ErrStalePlan
			}
			old, e := Decode(bytes.NewReader(current.Value), "json")
			if e != nil {
				return e
			}
			next, e := Decode(bytes.NewReader(plan.Effective), "json")
			if e != nil {
				return e
			}
			if e = Validate(next); e != nil {
				return e
			}

			for project, expected := range plan.Permissions {
				p, e := tx.Scope(project)
				if e != nil {
					return e
				}
				if p.Project.PermissionsRevision != expected {
					return domain.ErrPrecondition
				}
			}
			for project, setting := range next["projects"].(map[string]any) {
				v := setting.(map[string]any)
				p, e := tx.Scope(project)
				if e != nil {
					return e
				}
				name, _ := v["name"].(string)
				lifecycle, _ := v["lifecycle"].(string)
				_, change := plan.Permissions[project]
				if e = tx.ApplyProjectConfiguration(project, name, lifecycle, change, p.Project.PermissionsRevision); e != nil {
					return e
				}
			}
			// Global source policy can affect projects with no explicit override.
			for project, expected := range plan.Permissions {
				if _, included := next["projects"].(map[string]any)[project]; !included {
					if e = tx.ApplyProjectConfiguration(project, "", "", true, expected); e != nil {
						return e
					}
				}
			}
			// Complete current owner checks under the policy that authorizes this
			// switch. A shorter new expiry applies after the durable outcome commits.
			if _, e = s.owner(ctx, tx, token); e != nil {
				return e
			}
			if e = tx.SetConfiguration(domain.Configuration{Value: plan.Effective, Actor: actor.PrincipalID}, current.Revision); e != nil {
				return e
			}
			restart = !reflect.DeepEqual(old["network"], next["network"])
			response = domain.Response{Status: 200, Body: encoded(map[string]any{"plan_id": id, "config_revision": current.Revision + 1, "impact": json.RawMessage(plan.Impact)})}
			if e = tx.CompleteOperation(r.Key, response); e != nil {
				return e
			}
		}
		if alias != "" && alias != id {
			r.Key.OperationID = alias
			other, exists, e := tx.ReserveOperation(r, nil)
			if e != nil {
				return e
			}
			if exists {
				if other.Response == nil || !reflect.DeepEqual(*other.Response, response) {
					return domain.ErrConflict
				}
			} else if e = tx.CompleteOperation(r.Key, response); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return domain.Response{}, e
	}
	if restart && s.Restart != nil {
		defer s.Restart()
	}
	if e = s.Auth.Guard(ctx, token, func() error { return nil }); e != nil {
		return domain.Response{}, e
	}
	return response, nil
}
