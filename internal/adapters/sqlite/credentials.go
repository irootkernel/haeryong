package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
	"time"
)

func (t *writeTx) WorkflowPrincipal(id string) (domain.WorkflowPrincipal, error) {
	var p domain.WorkflowPrincipal
	var scope []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT principal_id,binding_id,active,scope,revision FROM workflow_principals WHERE principal_id=?", id).Scan(&p.ID, &p.BindingID, &p.Active, &scope, &p.Revision)
	if e != nil {
		return p, notFound(e)
	}
	e = json.Unmarshal(scope, &p.Scope)
	return p, e
}
func (t *writeTx) PutWorkflowPrincipal(p domain.WorkflowPrincipal) error {
	if p.ID == "" || p.BindingID == "" || p.Scope.Kind != "workflow" {
		return domain.ErrInvalidTarget
	}
	return t.exec("INSERT INTO workflow_principals VALUES(?,?,?,?,1) ON CONFLICT(principal_id) DO UPDATE SET active=excluded.active,scope=excluded.scope,revision=workflow_principals.revision+CASE WHEN workflow_principals.active IS NOT excluded.active OR workflow_principals.scope IS NOT excluded.scope THEN 1 ELSE 0 END WHERE workflow_principals.binding_id=excluded.binding_id", p.ID, p.BindingID, p.Active, jsonBytes(p.Scope))
}
func (t *writeTx) ScopedCredential(id string) (domain.ScopedCredential, error) {
	var c domain.ScopedCredential
	var scope, permissions []byte
	var created int64
	var expires sql.NullInt64
	e := t.tx.QueryRowContext(t.ctx, "SELECT credential_id,principal_id,token_hash,auth_epoch,scope,permissions,config_revision,created_at,expires_at,revoked,revision FROM scoped_credentials WHERE credential_id=?", id).Scan(&c.ID, &c.PrincipalID, &c.TokenHash, &c.AuthEpoch, &scope, &permissions, &c.ConfigRevision, &created, &expires, &c.Revoked, &c.Revision)
	if e != nil {
		return c, notFound(e)
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	if expires.Valid {
		v := time.UnixMilli(expires.Int64).UTC()
		c.ExpiresAt = &v
	}
	if e = json.Unmarshal(scope, &c.Scope); e != nil {
		return c, e
	}
	e = json.Unmarshal(permissions, &c.Permissions)
	return c, e
}
func (t *writeTx) ScopedCredentialByHash(hash string) (domain.ScopedCredential, error) {
	var c domain.ScopedCredential
	var scope, permissions []byte
	var created int64
	var expires sql.NullInt64
	e := t.tx.QueryRowContext(t.ctx, "SELECT credential_id,principal_id,token_hash,auth_epoch,scope,permissions,config_revision,created_at,expires_at,revoked,revision FROM scoped_credentials WHERE token_hash=?", hash).Scan(&c.ID, &c.PrincipalID, &c.TokenHash, &c.AuthEpoch, &scope, &permissions, &c.ConfigRevision, &created, &expires, &c.Revoked, &c.Revision)
	if e != nil {
		return c, notFound(e)
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	if expires.Valid {
		v := time.UnixMilli(expires.Int64).UTC()
		c.ExpiresAt = &v
	}
	if e = json.Unmarshal(scope, &c.Scope); e != nil {
		return c, e
	}
	e = json.Unmarshal(permissions, &c.Permissions)
	return c, e
}
func (t *writeTx) PutScopedCredential(c domain.ScopedCredential) error {
	if c.ID == "" || c.PrincipalID == "" || c.TokenHash == "" || c.Scope.Kind != "workflow" {
		return domain.ErrInvalidTarget
	}

	var oldHash string
	e := t.tx.QueryRowContext(t.ctx, "SELECT token_hash FROM scoped_credentials WHERE credential_id=?", c.ID).Scan(&oldHash)
	if e == nil {
		old, e := t.ScopedCredentialByHash(oldHash)
		if e != nil {
			return e
		}
		if old.PrincipalID != c.PrincipalID || old.TokenHash != c.TokenHash || old.AuthEpoch != c.AuthEpoch || !reflect.DeepEqual(old.Scope, c.Scope) || !reflect.DeepEqual(old.Permissions, c.Permissions) || old.ConfigRevision != c.ConfigRevision || !old.CreatedAt.Equal(c.CreatedAt) || !reflect.DeepEqual(old.ExpiresAt, c.ExpiresAt) {
			return domain.ErrConflict
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	} else {
		principal, e := t.WorkflowPrincipal(c.PrincipalID)
		if e != nil {
			return e
		}
		if !principal.Active {
			return domain.ErrDenied
		}
		state, e := t.AuthState()
		if e != nil {
			return e
		}
		if c.AuthEpoch != state.AuthEpoch {
			return domain.ErrDenied
		}
		for _, project := range c.Scope.ProjectIDs {
			scope, e := t.Scope(project)
			if e != nil {
				return e
			}
			revision, ok := c.Permissions[project]
			if !ok || revision != scope.Project.PermissionsRevision || c.ConfigRevision != scope.ConfigRevision {
				return domain.ErrDenied
			}
		}
		for _, action := range c.Scope.Actions {
			allowed := false
			for _, current := range principal.Scope.Actions {
				if action == current {
					allowed = true
				}
			}
			if !allowed {
				return domain.ErrDenied
			}
		}
		for _, pair := range [][2][]string{{c.Scope.ProjectIDs, principal.Scope.ProjectIDs}, {c.Scope.DraftIDs, principal.Scope.DraftIDs}, {c.Scope.VersionIDs, principal.Scope.VersionIDs}} {
			for _, id := range pair[0] {
				allowed := false
				for _, current := range pair[1] {
					if id == current {
						allowed = true
					}
				}
				if !allowed {
					return domain.ErrDenied
				}
			}
		}
	}
	var expiry any
	if c.ExpiresAt != nil {
		expiry = c.ExpiresAt.UnixMilli()
	}
	return t.exec("INSERT INTO scoped_credentials VALUES(?,?,?,?,?,?,?,?,?,?,1) ON CONFLICT(credential_id) DO UPDATE SET revoked=max(scoped_credentials.revoked,excluded.revoked),revision=scoped_credentials.revision+CASE WHEN excluded.revoked>scoped_credentials.revoked THEN 1 ELSE 0 END", c.ID, c.PrincipalID, c.TokenHash, c.AuthEpoch, jsonBytes(c.Scope), jsonBytes(c.Permissions), c.ConfigRevision, c.CreatedAt.UnixMilli(), expiry, c.Revoked)
}
