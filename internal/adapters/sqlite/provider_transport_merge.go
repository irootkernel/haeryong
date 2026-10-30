package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

// Merge audit observations independently of attempt-state precedence. A conflict
// returns both original facts to the existing orphan conflict-retention path.
func mergeProviderTransport(old, incoming *domain.ProviderTransport) (*domain.ProviderTransport, bool, error) {
	if old == nil {
		return incoming, false, nil
	}
	if incoming == nil {
		return old, false, nil
	}
	before, after := *old, *incoming
	before.LastLookup = nil
	after.LastLookup = nil
	before.AcceptedAt = ""
	after.AcceptedAt = ""
	before.FinishedAt = ""
	after.FinishedAt = ""
	before.BackendState = ""
	after.BackendState = ""
	before.BackendSource = ""
	after.BackendSource = ""
	before.DriverOutcome = ""
	after.DriverOutcome = ""
	before.Outcome = ""
	after.Outcome = ""
	before.CleanupState = ""
	after.CleanupState = ""
	before.CleanupReason = ""
	after.CleanupReason = ""
	before.LocalClosed = false
	after.LocalClosed = false
	if before.FrameSHA == "" {
		before.FrameSHA = after.FrameSHA
	}
	if after.FrameSHA == "" {
		after.FrameSHA = before.FrameSHA
	}
	if before.Capabilities == nil {
		before.Capabilities = after.Capabilities
	}
	if after.Capabilities == nil {
		after.Capabilities = before.Capabilities
	}
	a, e := json.Marshal(before)
	if e != nil {
		return nil, false, e
	}
	b, e := json.Marshal(after)
	if e != nil {
		return nil, false, e
	}
	if string(a) != string(b) {
		return incoming, true, nil
	}
	merged := *old
	merged.FrameSHA = before.FrameSHA
	merged.Capabilities = before.Capabilities
	first := func(a, b string) string {
		if a == "" {
			return b
		}
		if b == "" {
			return a
		}
		ta, ea := time.Parse(time.RFC3339Nano, a)
		tb, eb := time.Parse(time.RFC3339Nano, b)
		if ea == nil && eb == nil && tb.Before(ta) {
			return b
		}
		return a
	}
	merged.AcceptedAt = first(old.AcceptedAt, incoming.AcceptedAt)
	merged.FinishedAt = first(old.FinishedAt, incoming.FinishedAt)
	if merged.Outcome == "" {
		merged.Outcome = incoming.Outcome
	} else if incoming.Outcome != "" && merged.Outcome != incoming.Outcome {
		return incoming, true, nil
	}
	pick := func(a, b *domain.ProviderLookup) (*domain.ProviderLookup, bool) {
		if a == nil {
			return b, false
		}
		if a.BackendState == "" {
			return b, false
		}
		if b != nil && b.BackendState == "" {
			return a, false
		}
		if b == nil {
			return a, false
		}
		if reliableLookup(*a) && reliableLookup(*b) && a.BackendState != b.BackendState {
			return b, true
		}
		if reliableLookup(*a) && !reliableLookup(*b) {
			return a, false
		}
		if reliableLookup(*b) && !reliableLookup(*a) {
			return b, false
		}
		ta, ea := time.Parse(time.RFC3339Nano, a.ObservedAt)
		tb, eb := time.Parse(time.RFC3339Nano, b.ObservedAt)
		if ea == nil && eb == nil && tb.After(ta) {
			return b, false
		}
		return a, false
	}
	lookup, conflict := pick(old.LastLookup, incoming.LastLookup)
	if conflict {
		return incoming, true, nil
	}
	merged.LastLookup = lookup
	core, conflict := pick(&domain.ProviderLookup{BackendState: old.BackendState, Source: old.BackendSource, ObservedAt: old.FinishedAt}, &domain.ProviderLookup{BackendState: incoming.BackendState, Source: incoming.BackendSource, ObservedAt: incoming.FinishedAt})
	if conflict {
		return incoming, true, nil
	}
	merged.BackendState = core.BackendState
	merged.BackendSource = core.Source
	merged.FinishedAt = core.ObservedAt
	if incoming.FinishedAt != "" && (merged.DriverOutcome == "" || merged.DriverOutcome == "outcome_unknown") {
		merged.DriverOutcome = incoming.DriverOutcome
	}
	if incoming.LocalClosed || !old.LocalClosed {
		merged.LocalClosed = incoming.LocalClosed
		merged.CleanupState = incoming.CleanupState
		merged.CleanupReason = incoming.CleanupReason
	}
	return &merged, false, nil
}
