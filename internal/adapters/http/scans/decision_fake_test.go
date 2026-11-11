package scans

import "haeryong.local/haeryong/internal/domain"

// This loopback fake has no owner decision state; production uses SQLite.
func (t *fakeScanTx) TargetOwnerRevision(string) (int64, error) { return 0, nil }
func (t *fakeScanTx) FindingDecisionBasis(string) (domain.FindingDecisionBasis, bool, error) {
	return domain.FindingDecisionBasis{}, false, nil
}
