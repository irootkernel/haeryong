package storage

import "haeryong.local/haeryong/internal/domain"

type ReviewTx interface {
	Review(owner, version string) (domain.Review, error)
	SaveReview(domain.Review, domain.ReviewEvent) error
	ReviewHistory(owner, version string, after int64, limit int) ([]domain.ReviewEvent, error)
	ReviewSummary(owner, document string) (domain.ReviewSummary, error)
	QueryReviewDocuments(owner, project, lifecycle, review, afterUpdated, afterID string, limit int) ([]domain.QueryDocument, error)
}
