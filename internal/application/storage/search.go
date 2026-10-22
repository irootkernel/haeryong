package storage

import "haeryong.local/haeryong/internal/domain"

// SearchTx owns derived persistence and bounded maintenance operations.
// Callbacks remain short; extraction and object reads belong outside Write.
type SearchTx interface {
	EnqueueSearch(string, int64) (string, error)
	SearchVersions(string, int) ([]string, error)
	SuspendSearch(string, int64) error
	ResumeSearch(string, int64, bool) error
	SearchFence(string, int64) (domain.DerivedWork, error)
	SearchInventory(string, string, string, int) ([]domain.SearchInventoryItem, error)
	NextSearchPage(string) (domain.SearchUnit, error)
	SearchRegionUnit(string, string, string) (domain.SearchUnit, error)
	ClearSearchData(string, string, int) (bool, error)
	DeleteDerivedVersion(string, int) (bool, error)
	SearchProgress(string, string) error
	SearchIndexUpdated(string) error
	CompleteSearchPage(int64) error
	FailSearch(string, string) error
	FailSearchPage(string, int64, int64, string) error
	PutDerivedWork(domain.DerivedWork) error
	DerivedWork(string) (domain.DerivedWork, error)
	PutSearchIndex(domain.SearchIndex) error
	SearchIndex(string) (domain.SearchIndex, error)
	PutSearchUnit(domain.SearchUnit) (int64, error)
	SearchUnit(int64) (domain.SearchUnit, error)
	PutSearchChunk(domain.SearchChunk) error
	MergeSearch(int, int) error
	PutPageAnchor(domain.PageAnchor) error
	PageAnchor(string, string, string) (domain.PageAnchor, error)
}
