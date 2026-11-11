package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) ManagementEffect(id string) (domain.ManagementEffect, error) {
	var m domain.ManagementEffect
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM overlay_relations WHERE effect_id=? UNION ALL SELECT value FROM topic_assignments WHERE effect_id=?", id, id).Scan(&raw)
	if e != nil {
		return m, notFound(e)
	}
	e = json.Unmarshal(raw, &m)
	return m, e
}
func (t *writeTx) ManagementEffectByKey(key string) (domain.ManagementEffect, bool, error) {
	var m domain.ManagementEffect
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM (SELECT value,revision,decision,pinned FROM overlay_relations WHERE semantic_key=? UNION ALL SELECT value,revision,decision,pinned FROM topic_assignments WHERE semantic_key=?) ORDER BY pinned DESC,CASE decision WHEN 'rejected' THEN 0 WHEN 'accepted' THEN 1 ELSE 2 END,revision DESC LIMIT 1", key, key).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return m, false, nil
	}
	if e != nil {
		return m, false, e
	}
	e = json.Unmarshal(raw, &m)
	return m, true, e
}
func (t *writeTx) ManagementLinkedFindings(id string) ([]domain.FindingEffectLink, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT l.finding_id,l.effect_id,l.effect_revision,COALESCE(s.finding_revision,f.revision),COALESCE(s.active,CASE WHEN f.status='applied' THEN 1 ELSE 0 END),EXISTS(SELECT 1 FROM management_effect_history h WHERE h.effect_id=l.effect_id AND h.kind=l.kind AND h.revision=json_extract(f.applied_ref_json,'$.revision') AND json_extract(f.applied_ref_json,'$.overlay_id')=l.effect_id AND json_extract(f.applied_ref_json,'$.kind')=l.kind AND json_extract(h.value,'$.origin')='ai') FROM finding_effects l LEFT JOIN findings f USING(finding_id) LEFT JOIN management_link_states s USING(finding_id) WHERE l.effect_id=? ORDER BY l.finding_id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.FindingEffectLink{}
	for rows.Next() {
		var l domain.FindingEffectLink
		if e = rows.Scan(&l.FindingID, &l.EffectID, &l.EffectRevision, &l.FindingRevision, &l.Active, &l.ValidHistory); e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (t *writeTx) SaveManagementLink(l domain.FindingEffectLink) error {
	if e := t.exec("UPDATE finding_effects SET effect_revision=? WHERE finding_id=? AND effect_id=?", l.EffectRevision, l.FindingID, l.EffectID); e != nil {
		return e
	}
	return t.exec("INSERT INTO management_link_states VALUES(?,?,?,?) ON CONFLICT(finding_id) DO UPDATE SET effect_id=excluded.effect_id,finding_revision=excluded.finding_revision,active=excluded.active", l.FindingID, l.EffectID, l.FindingRevision, boolInt(l.Active))
}
func (t *writeTx) FindingDecision(key string) (domain.FindingDecision, bool, error) {
	var d domain.FindingDecision
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM management_decisions WHERE decision_key=?", key).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return d, false, nil
	}
	if e != nil {
		return d, false, e
	}
	e = json.Unmarshal(raw, &d)
	return d, true, e
}
func (t *writeTx) SaveFindingDecision(d domain.FindingDecision, expected int64) error {
	old, found, e := t.FindingDecision(d.Key)
	if e != nil {
		return e
	}
	if found && old.Revision != expected || !found && expected != 0 || d.Revision != expected+1 {
		return domain.ErrPrecondition
	}
	if e = t.exec("INSERT INTO management_decisions VALUES(?,?,?) ON CONFLICT(decision_key) DO UPDATE SET revision=excluded.revision,value=excluded.value", d.Key, d.Revision, jsonBytes(d)); e != nil {
		return e
	}
	return t.exec("INSERT INTO management_decision_history VALUES(?,?,?)", d.Key, d.Revision, jsonBytes(d))
}
func (t *writeTx) FindingDecisionBasis(id string) (domain.FindingDecisionBasis, bool, error) {
	var b domain.FindingDecisionBasis
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM finding_decision_bases WHERE finding_id=?", id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return b, false, nil
	}
	if e != nil {
		return b, false, e
	}
	e = json.Unmarshal(raw, &b)
	return b, true, e
}
func (t *writeTx) SaveFindingDecisionBasis(b domain.FindingDecisionBasis) error {
	return t.exec("INSERT INTO finding_decision_bases VALUES(?,?) ON CONFLICT(finding_id) DO NOTHING", b.FindingID, jsonBytes(b))
}

// Capture exact semantic decisions touching the retained version. Keys already
// include kind, source/target anchors and topic/mechanism; no version-wide ban.
func (t *writeTx) FindingOwnerRevisions(version string) (map[string]int64, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT decision_key,revision FROM management_decisions WHERE EXISTS(SELECT 1 FROM json_each(json_extract(management_decisions.value,'$.version_ids')) v WHERE v.value=?)`, version)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var key string
		var revision int64
		if e = rows.Scan(&key, &revision); e != nil {
			return nil, e
		}
		out[key] = revision
	}
	return out, rows.Err()
}
func (t *writeTx) TargetOwnerRevision(version string) (int64, error) {
	var n int64
	e := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(sum(revision),0) FROM management_decisions WHERE EXISTS(SELECT 1 FROM json_each(json_extract(management_decisions.value,'$.version_ids')) v WHERE v.value=?)`, version).Scan(&n)
	return n, e
}
func (t *writeTx) ReviseFinding(f domain.Finding, expected int64) error {
	old, e := t.Finding(f.ID)
	if e != nil {
		return e
	}
	if old.Revision != expected || f.Revision != expected+1 {
		return domain.ErrPrecondition
	}
	return t.PutFinding(f)
}
func (t *writeTx) purgeFindingDecisions(document string) error {
	where := func(table string) string {
		return `EXISTS(SELECT 1 FROM json_each(json_extract(` + table + `.value,'$.version_ids')) v JOIN purged_versions p ON p.version_id=v.value WHERE p.document_id=?)`
	}
	if e := t.exec("DELETE FROM finding_decision_bases WHERE json_extract(value,'$.key') IN(SELECT decision_key FROM management_decision_history WHERE "+where("management_decision_history")+") OR finding_id IN(SELECT finding_id FROM findings WHERE document_id=?)", document, document); e != nil {
		return e
	}
	for _, table := range []string{"management_decision_history", "management_decisions"} {
		if e := t.exec("DELETE FROM "+table+" WHERE "+where(table), document); e != nil {
			return e
		}
	}
	return t.exec("DELETE FROM management_link_states WHERE finding_id IN(SELECT finding_id FROM findings WHERE document_id=?) OR effect_id IN(SELECT effect_id FROM overlay_relations WHERE json_extract(value,'$.from.document_id')=? OR json_extract(value,'$.target.document_id')=?)", document, document, document)
}
