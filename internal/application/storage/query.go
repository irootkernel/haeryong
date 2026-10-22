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

// SearchReadTx streams candidates from the eligible ready corpus, without a
// hidden first-N cap. Each physical chunk slice is authenticated; grouping/ranking runs outside the lock.
type SearchReadTx interface {
	SearchCorpus(domain.SearchSelection) ([]domain.SearchVersionState, error)
	SearchHighWater() (int64, error)
	SearchSnapshotSlice(domain.SearchSelection, string, int64) (domain.SearchSnapshotSlice, error)
	SearchReadStamp(domain.SearchSelection) (string, error)
	SearchAuthorityStamp(domain.Session) (string, error)
	SearchCandidates(domain.SearchSelection, string, int64, int64) (domain.SearchSlice, error)
}
