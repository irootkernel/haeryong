package domain

// Annotation revisions retain their exact anchors and immutable object ownership.
type AnnotationAnchor struct {
	VersionID    string  `json:"version_id"`
	PagePath     *string `json:"page_path"`
	RegionID     *string `json:"region_id"`
	PageQuery    string  `json:"page_query"`
	PageFragment string  `json:"page_fragment"`
}
type Annotation struct {
	ID             string           `json:"annotation_id"`
	OwnerID        string           `json:"-"`
	DocumentID     string           `json:"document_id"`
	OriginalAnchor AnnotationAnchor `json:"original_anchor"`
	Anchor         AnnotationAnchor `json:"anchor"`
	Body           *string          `json:"body"`
	BodyObject     *Object          `json:"body_object"`
	SelectedText   *string          `json:"selected_text"`
	ContextRef     *string          `json:"context_ref"`
	CreatedAt      string           `json:"created_at"`
	UpdatedAt      string           `json:"updated_at"`
	DeletedAt      *string          `json:"deleted_at"`
	Revision       int64            `json:"revision"`
	Action         string           `json:"action"`
	ActorSession   string           `json:"actor_session"`
	OperationID    string           `json:"operation_id"`
}
type Revisit struct {
	OwnerID, DocumentID string
	Flag                bool
	Revision            int64
}
