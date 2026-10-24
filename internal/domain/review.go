package domain

// Review is personal state for one whole immutable directory version.
type Review struct {
	OwnerID, VersionID, State string
	ReviewedAt                *string
	Revision, EventSequence   int64
}

type ReviewEvent struct {
	Sequence     int64  `json:"-"`
	ID           string `json:"event_id"`
	VersionID    string `json:"version_id"`
	Action       string `json:"action"`
	OldState     string `json:"old_state"`
	NewState     string `json:"new_state"`
	ActorSession string `json:"actor_session"`
	OperationID  string `json:"operation_id"`
	CreatedAt    string `json:"created_at"`
}

type ReviewSummary struct {
	State                 *string `json:"review_state"`
	ReviewedAt            *string `json:"reviewed_at"`
	LastReviewedVersionID *string `json:"last_reviewed_version_id"`
	UpdatedSinceReview    *bool   `json:"updated_since_review"`
	ContinueVersionID     *string `json:"continue_review_version_id"`
}
