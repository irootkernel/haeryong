package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) StagingSummary() (domain.StagingSummary, error) {
	out := domain.StagingSummary{PendingUploadsURL: "/api/v1/uploads"}
	err := t.tx.QueryRowContext(t.ctx, `SELECT
	 (SELECT count(*) FROM uploads WHERE state IN ('open','sealing','sealed')),
	 coalesce(cast(sum(byte_size) AS TEXT),'0') FROM content_objects o
	 WHERE EXISTS (SELECT 1 FROM object_pins p JOIN uploads u ON u.upload_id=p.owner_id
	 WHERE p.owner_kind='upload' AND p.content_hash=o.content_hash AND u.state IN ('open','sealing','sealed'))`).Scan(&out.PendingUploads, &out.Bytes)
	return out, err
}

func (t *writeTx) Configuration() (domain.Configuration, error) {
	var c domain.Configuration
	e := t.tx.QueryRowContext(t.ctx, "SELECT config_revision,validated_json,actor FROM config_revisions ORDER BY config_revision DESC LIMIT 1").Scan(&c.Revision, &c.Value, &c.Actor)
	return c, e
}
func (t *writeTx) ConfigurationPlan(id string) (domain.ConfigurationPlan, error) {
	var p domain.ConfigurationPlan
	var value []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT principal_id,value FROM configuration_plans WHERE plan_id=?", id).Scan(&p.PrincipalID, &value)
	if e != nil {
		return p, notFound(e)
	}
	var saved struct {
		Plan      domain.ConfigurationPlan
		Effective json.RawMessage
	}
	e = json.Unmarshal(value, &saved)
	saved.Plan.PrincipalID = p.PrincipalID
	saved.Plan.Effective = saved.Effective
	return saved.Plan, e
}
func (t *writeTx) PutConfigurationPlan(p domain.ConfigurationPlan) error {
	return t.exec("INSERT INTO configuration_plans VALUES(?,?,?,?,?)", p.ID, p.PrincipalID, p.BaseRevision, p.ExpiresAt, jsonBytes(struct {
		Plan      domain.ConfigurationPlan
		Effective json.RawMessage
	}{p, p.Effective}))
}
func (t *writeTx) ApplyProjectConfiguration(id, name, lifecycle string, permissions bool, expected int64) error {
	p, e := t.Scope(id)
	if e != nil {
		return e
	}
	if p.Project.PermissionsRevision != expected {
		return domain.ErrPrecondition
	}
	if name == "" {
		name = p.Project.Name
	}
	if lifecycle == "" {
		lifecycle = p.Project.Lifecycle
	}
	if name == p.Project.Name && lifecycle == p.Project.Lifecycle && !permissions {
		return nil
	}
	advance := 0
	if permissions {
		advance = 1
	}
	if e = t.exec("UPDATE projects SET name=?,lifecycle=?,revision=revision+1,permissions_revision=permissions_revision+? WHERE project_id=?", name, lifecycle, advance, id); e != nil {
		return e
	}
	return t.appendEvent("project", id, "project.updated", p.Project.Revision+1, nil)
}
