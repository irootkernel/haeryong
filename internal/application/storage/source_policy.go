package storage

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

// ManagedSourcePolicy checks current remembered authority without I/O or an
// interactive prompt. Provider disclosure is distinct from owner source reading.
func ManagedSourcePolicy(tx WriteTx, project, sourceID, provider string) (domain.ManagedSourceMapping, error) {
	var zero domain.ManagedSourceMapping
	if _, e := tx.Scope(project); e != nil {
		return zero, e
	}
	hold, e := tx.RecoveryHold()
	if e != nil {
		return zero, e
	}
	if hold != nil && hold.State != "reviewed" {
		return zero, domain.ErrReconciliation
	}
	cfg, e := tx.Configuration()
	if e != nil {
		return zero, e
	}
	var value struct {
		Projects map[string]struct {
			Sources []domain.ManagedSourceMapping `json:"sources"`
		} `json:"projects"`
	}
	if e = json.Unmarshal(cfg.Value, &value); e != nil {
		return zero, e
	}
	for _, mapping := range value.Projects[project].Sources {
		if mapping.SourceID == sourceID {
			if provider != "" {
				allowed := false
				for _, id := range mapping.ProviderIDs {
					if id == provider {
						allowed = true
					}
				}
				if !allowed {
					return zero, domain.ErrDenied
				}
			}
			return mapping, nil
		}
	}
	return zero, domain.ErrDenied
}
