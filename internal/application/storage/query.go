package storage

import "haeryong.local/haeryong/internal/domain"

// QueryReadTx supplies bounded reads in the same transaction as current
// authentication and target visibility checks. No SQL handle crosses this port.
type QueryReadTx interface {
	QueryProject(id string) (domain.QueryProject, error)
	QueryDocument(id string) (domain.QueryDocument, error)
	QueryProjects(scope domain.CredentialScope, afterUpdated, afterID string, limit int) ([]domain.QueryProject, error)
	QueryDocuments(scope domain.CredentialScope, project, lifecycle, afterUpdated, afterID string, limit int) ([]domain.QueryDocument, error)
	QueryVersions(scope domain.CredentialScope, document string, afterSequence int64, limit int) ([]domain.Version, error)
	QueryEntry(contentSet, path string) (domain.Entry, error)
	QueryEntries(contentSet, after, directory, kind string, limit int) ([]domain.Entry, error)
	QuerySource(version, id string) (domain.Source, error)
	QueryEvidence(version, id string) (domain.Evidence, error)
}
