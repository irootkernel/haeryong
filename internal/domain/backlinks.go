package domain

// EdgeKey is an internal physical cursor. Nullable anchors retain SQLite's
// NULL-first index order; raw progress is never a public visibility cursor.
type EdgeKey struct {
	RowID                    int64
	TargetVersion            string
	Page, Region             *string
	SourceVersion, Reference string
}
