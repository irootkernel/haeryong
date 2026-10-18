package sqlite

import (
	"database/sql"
	"errors"

	"haeryong.local/haeryong/internal/domain"
)

// ResourceRow reads a mutable personal resource inside the caller's write
// transaction so opaque validators compare against the exact stored state.
func (t *writeTx) ResourceRow(kind, id string) (domain.Resource, error) {
	return resource(t.ctx, t.tx, kind, id)
}

func nullableText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (t *writeTx) PutContext(c domain.ContextRecord) error {
	if c.ID == "" || c.PrincipalID == "" || c.VersionID == "" || c.SessionID == "" || (c.MediaType != "application/json" && c.MediaType != "text/plain") || !hashPattern.MatchString(c.Object.Hash) || c.Object.Size < 0 || c.CreatedAt == "" {
		return errors.New("invalid context record")
	}
	if c.PagePath != nil && *c.PagePath == "" {
		return errors.New("invalid context page binding")
	}
	return t.exec("INSERT INTO contexts(context_id,principal_id,version_id,page_path,render_instance_id,origin_session_id,media_type,content_hash,byte_size,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", c.ID, c.PrincipalID, c.VersionID, nullableText(c.PagePath), nullableText(c.RenderInstanceID), c.SessionID, c.MediaType, c.Object.Hash, c.Object.Size, c.CreatedAt)
}

func contextRecord(row *sql.Row) (domain.ContextRecord, error) {
	var c domain.ContextRecord
	var page, render any
	if err := row.Scan(&c.ID, &c.PrincipalID, &c.VersionID, &page, &render, &c.SessionID, &c.MediaType, &c.Object.Hash, &c.Object.Size, &c.CreatedAt); err != nil {
		return c, notFound(err)
	}
	if page != nil {
		text, ok := page.(string)
		if !ok || text == "" {
			return c, errors.New("invalid stored context page binding")
		}
		c.PagePath = &text
	}
	if render != nil {
		text, ok := render.(string)
		if !ok || text == "" {
			return c, errors.New("invalid stored context render binding")
		}
		c.RenderInstanceID = &text
	}
	c.Object.State = "ready"
	return c, nil
}

func (t *writeTx) Context(id string) (domain.ContextRecord, error) {
	return contextRecord(t.tx.QueryRowContext(t.ctx, "SELECT context_id,principal_id,version_id,page_path,render_instance_id,origin_session_id,media_type,content_hash,byte_size,created_at FROM contexts WHERE context_id=?", id))
}
