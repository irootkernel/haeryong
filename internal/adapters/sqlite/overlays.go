package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/domain"
)

func managementTable(kind string) (string, error) {
	switch kind {
	case "related_explanation":
		return "overlay_relations", nil
	case "topic_assignment":
		return "topic_assignments", nil
	}
	return "", domain.ErrInvalidTarget
}

// The caller owns current authority/lifecycle/policy checks in the same writer.
// Active semantic-key constraints cover distinct findings, runs and children.
func (t *writeTx) PutManagementEffect(m domain.ManagementEffect) (domain.ManagementEffect, bool, error) {
	table, e := managementTable(m.Kind)
	if e != nil {
		return m, false, e
	}
	if m.Availability != "" || m.ID == "" || m.From.VersionID == "" || m.From.DocumentID == "" || m.Origin != "ai" || m.Decision != "accepted" || m.Revision != 1 || m.Rationale == "" || m.DecisionActor == "" || m.From.RegionID != nil && m.From.PagePath == nil {
		return m, false, domain.ErrInvalidTarget
	}
	if m.Kind == "related_explanation" && (m.Target == nil || m.Target.VersionID == "" || m.Target.DocumentID == "" || m.Target.RegionID != nil && m.Target.PagePath == nil || m.RelationType == "" || m.MechanismKey == "") {
		return m, false, domain.ErrInvalidTarget
	}
	if m.Kind == "topic_assignment" && (m.Topic == "" || m.TopicID != domain.ManagementDigest(m.Topic) || domain.TopicEvidenceBasis(m.EvidenceBasis) == "legacy_unknown") {
		return m, false, domain.ErrInvalidTarget
	}
	m.SemanticKey = m.Key()
	m.ETag = m.Validator()
	var raw []byte
	e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM `+table+` WHERE semantic_key=? AND (decision IN ('accepted','rejected','undone') OR pinned=1) ORDER BY CASE WHEN pinned=1 OR decision='rejected' THEN 0 ELSE 1 END,effect_id LIMIT 1`, m.SemanticKey).Scan(&raw)
	if e == nil {
		var old domain.ManagementEffect
		if e = json.Unmarshal(raw, &old); e != nil {
			return m, false, e
		}
		if old.Pinned || old.Decision != "accepted" {
			return old, false, domain.ErrConflict
		}
		return old, false, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return m, false, e
	}
	if m.Kind == "topic_assignment" {
		if e = t.exec(`INSERT OR IGNORE INTO topics(topic_id,label) VALUES(?,?)`, m.TopicID, m.Topic); e != nil {
			return m, false, e
		}
		e = t.exec(`INSERT INTO topic_assignments VALUES(?,?,?,?,?,?,?,?)`, m.ID, m.SemanticKey, m.From.VersionID, m.TopicID, m.Decision, boolInt(m.Pinned), m.Revision, jsonBytes(m))
	} else {
		e = t.exec(`INSERT INTO overlay_relations VALUES(?,?,?,?,?,?,?,?)`, m.ID, m.SemanticKey, m.From.VersionID, m.Target.VersionID, m.Decision, boolInt(m.Pinned), m.Revision, jsonBytes(m))
	}
	if e != nil {
		return m, false, e
	}
	e = t.exec(`INSERT INTO management_effect_history VALUES(?,?,?,?)`, m.ID, m.Revision, m.Kind, jsonBytes(m))
	return m, true, e
}

func (t *writeTx) LinkManagementFinding(f domain.Finding, m domain.ManagementEffect) error {
	// Keep every finding -> effect revision for Task05's all-linked undo checks.
	if e := t.exec(`INSERT INTO finding_effects VALUES(?,?,?,?) ON CONFLICT(finding_id) DO NOTHING`, f.ID, m.ID, m.Kind, m.Revision); e != nil {
		return e
	}
	var id, kind string
	var revision int64
	if e := t.tx.QueryRowContext(t.ctx, `SELECT effect_id,kind,effect_revision FROM finding_effects WHERE finding_id=?`, f.ID).Scan(&id, &kind, &revision); e != nil {
		return e
	}
	if id != m.ID || kind != m.Kind || revision != m.Revision {
		return domain.ErrConflict
	}
	return t.SaveManagementLink(domain.FindingEffectLink{FindingID: f.ID, EffectID: m.ID, EffectRevision: m.Revision, FindingRevision: f.Revision, Active: true})
}

func (t *writeTx) ValidateManagementFinding(f domain.Finding) error {
	table, err := managementTable(f.Kind)
	if err != nil {
		return err
	}
	var ref struct {
		ID       string `json:"overlay_id"`
		Kind     string `json:"kind"`
		Revision int64  `json:"revision"`
	}
	if json.Unmarshal(f.AppliedRef, &ref) != nil || ref.ID == "" || ref.Kind != f.Kind || ref.Revision < 1 {
		return domain.ErrReconciliation
	}
	var valid bool
	err = t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM finding_effects l JOIN management_effect_history h ON h.effect_id=l.effect_id AND h.revision=l.effect_revision AND h.kind=l.kind JOIN `+table+` m ON m.effect_id=l.effect_id WHERE l.finding_id=? AND l.effect_id=? AND l.kind=? AND l.effect_revision=? AND m.decision='accepted' AND m.revision>=l.effect_revision AND json_extract(h.value,'$.origin')='ai')`, f.ID, ref.ID, ref.Kind, ref.Revision).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return domain.ErrReconciliation
	}
	return nil
}

// Repository revision seam, not an owner decision/undo service. Future Task05
// must guard authority and all linked findings before using this transaction.
func (t *writeTx) ReviseManagementEffect(m domain.ManagementEffect, expected int64) error {
	table, e := managementTable(m.Kind)
	if e != nil {
		return e
	}
	var raw []byte
	if e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM `+table+` WHERE effect_id=? AND revision=?`, m.ID, expected).Scan(&raw); e != nil {
		return notFound(e)
	}
	var old domain.ManagementEffect
	if e = json.Unmarshal(raw, &old); e != nil {
		return e
	}
	allowed := old
	allowed.Decision = m.Decision
	allowed.DecisionActor = m.DecisionActor
	allowed.Pinned = m.Pinned
	allowed.Revision = m.Revision
	allowed.UpdatedAt = m.UpdatedAt
	allowed.ETag = m.Validator()
	m.ETag = m.Validator()
	if old.Availability == "unavailable" || m.Revision != expected+1 || old.Origin != "ai" || domain.ManagementDigest(m) != domain.ManagementDigest(allowed) {
		return domain.ErrConflict
	}
	if e = t.exec(`UPDATE `+table+` SET decision=?,pinned=?,revision=?,value=? WHERE effect_id=? AND revision=?`, m.Decision, boolInt(m.Pinned), m.Revision, jsonBytes(m), m.ID, expected); e != nil {
		return e
	}
	return t.exec(`INSERT INTO management_effect_history VALUES(?,?,?,?)`, m.ID, m.Revision, m.Kind, jsonBytes(m))
}

func (t *writeTx) ManagementEffects(f domain.ManagementFilter, after string, limit int) ([]domain.ManagementEffect, error) {
	table, e := managementTable(f.Kind)
	if e != nil {
		return nil, e
	}
	q := `SELECT value FROM ` + table + ` WHERE effect_id>?`
	args := []any{after}
	if f.VersionID != "" {
		q += ` AND version_id=?`
		args = append(args, f.VersionID)
	}
	if f.DocumentID != "" {
		q += ` AND json_extract(value,'$.from.document_id')=?`
		args = append(args, f.DocumentID)
	}
	if f.PagePath != nil {
		q += ` AND json_extract(value,'$.from.page_path')=?`
		args = append(args, *f.PagePath)
	}
	if f.RegionID != nil {
		q += ` AND json_extract(value,'$.from.region_id')=?`
		args = append(args, *f.RegionID)
	}
	q += ` ORDER BY effect_id LIMIT ?`
	args = append(args, limit)
	rows, e := t.tx.QueryContext(t.ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.ManagementEffect{}
	for rows.Next() {
		var raw []byte
		var m domain.ManagementEffect
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Purge scrubs every sensitive payload/history/link referring to the removed
// version. A surviving focal relation retains only a nondisclosing marker.
func (t *writeTx) purgeManagement(document string) error {
	if e := t.purgeFindingDecisions(document); e != nil {
		return e
	}
	for _, table := range []string{"overlay_relations", "topic_assignments"} {
		where := `json_extract(value,'$.from.document_id')=? OR json_extract(value,'$.target.document_id')=? OR EXISTS(SELECT 1 FROM json_each(json_extract(` + table + `.value,'$.evidence')) ev WHERE json_extract(ev.value,'$.version_id') IN(SELECT version_id FROM purged_versions WHERE document_id=?))`
		args := []any{document, document, document}
		for _, prefix := range []string{`DELETE FROM management_link_states WHERE effect_id IN(SELECT effect_id FROM `, `DELETE FROM finding_effects WHERE effect_id IN(SELECT effect_id FROM `, `DELETE FROM management_effect_history WHERE effect_id IN(SELECT effect_id FROM `} {
			if e := t.exec(prefix+table+` WHERE `+where+`)`, args...); e != nil {
				return e
			}
		}
		if table == "overlay_relations" {
			rows, e := t.tx.QueryContext(t.ctx, `SELECT value FROM overlay_relations WHERE `+where, args...)
			if e != nil {
				return e
			}
			var effects []domain.ManagementEffect
			for rows.Next() {
				var raw []byte
				var m domain.ManagementEffect
				if e = rows.Scan(&raw); e == nil {
					e = json.Unmarshal(raw, &m)
				}
				if e != nil {
					rows.Close()
					return e
				}
				effects = append(effects, m)
			}
			if e = errors.Join(rows.Err(), rows.Close()); e != nil {
				return e
			}
			for _, m := range effects {
				if m.From.DocumentID == document {
					if e = t.exec(`DELETE FROM overlay_relations WHERE effect_id=?`, m.ID); e != nil {
						return e
					}
					continue
				}
				m = m.Unavailable()
				m.Revision++
				// Neither the old semantic digest nor the target column survives.
				key := domain.ManagementDigest([]string{"unavailable", m.ID})
				if e = t.exec(`UPDATE overlay_relations SET semantic_key=?,target_version_id='',revision=?,value=? WHERE effect_id=?`, key, m.Revision, jsonBytes(m), m.ID); e != nil {
					return e
				}
			}
		} else if e := t.exec(`DELETE FROM `+table+` WHERE `+where, args...); e != nil {
			return e
		}
	}
	return t.exec(`DELETE FROM topics WHERE NOT EXISTS(SELECT 1 FROM topic_assignments a WHERE a.topic_id=topics.topic_id)`)
}

func (t *writeTx) managementImpact(document string) ([]domain.LifecycleItem, error) {
	out := []domain.LifecycleItem{}
	for _, table := range []string{"overlay_relations", "topic_assignments"} {
		rows, e := t.tx.QueryContext(t.ctx, `SELECT effect_id,version_id,revision FROM `+table+`,(SELECT ? AS doc_id) d WHERE json_extract(value,'$.from.document_id')=d.doc_id OR json_extract(value,'$.target.document_id')=d.doc_id OR EXISTS(SELECT 1 FROM json_each(json_extract(`+table+`.value,'$.evidence')) ev WHERE json_extract(ev.value,'$.version_id') IN(SELECT version_id FROM document_versions WHERE document_id=d.doc_id)) ORDER BY effect_id`, document)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id, v string
			var revision int64
			if e = rows.Scan(&id, &v, &revision); e != nil {
				rows.Close()
				return nil, e
			}
			out = append(out, domain.LifecycleItem{Kind: "management_effect", ID: id, VersionID: &v, Revision: revision})
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return nil, e
		}
	}
	return out, nil
}
