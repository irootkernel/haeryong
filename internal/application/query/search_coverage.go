package query

import (
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/domain"
	"sort"
	"strconv"
)

// safeIndexReason is the only persisted-failure to public-code mapper. Raw stored
// diagnostics, paths and job bodies never cross this projection.
func safeIndexReason(reason string) string {
	if oneOf(reason, "interrupted", "cancelled", "read_failed", "decode_failed", "integrity_failed", "resource_exhausted", "storage_failed") {
		return reason
	}
	return "failed_unknown"
}
func searchCoverage(corpus []domain.SearchVersionState) SearchCoverage {
	out := SearchCoverage{State: "empty", DiscoveryComplete: true, Limitations: []string{"static_only"}, Reasons: []IndexReason{}}
	var complete, files, discovered, total, done, failed, unsupported int64
	counts := map[string]int64{}
	for _, v := range corpus {
		files += v.FilesTotal
		discovered += v.FilesDiscovered
		total += v.HTMLTotal
		done += v.HTMLComplete
		failed += v.HTMLFailed
		unsupported += v.Unsupported
		if v.IndexState == "complete" && v.DiscoveryComplete && v.HTMLTotal == v.HTMLComplete {
			complete++
		}
		out.DiscoveryComplete = out.DiscoveryComplete && v.DiscoveryComplete
		reasons := map[string]bool{}
		if v.IndexRevision == 0 || !v.DiscoveryComplete || v.HTMLComplete+v.HTMLFailed < v.HTMLTotal {
			reasons["not_indexed"] = true
		}
		if v.Unsupported > 0 {
			reasons["unsupported_body"] = true
		}
		for _, r := range v.UnitReasons {
			reasons[safeIndexReason(r)] = true
		}
		if v.Reason != "" {
			reasons[safeIndexReason(v.Reason)] = true
		} else if v.IndexState == "failed" && len(v.UnitReasons) == 0 {
			reasons["failed_unknown"] = true
		}
		for r := range reasons {
			counts[r]++
		}
	}
	if len(corpus) > 0 {
		out.State = "partial"
		if complete == int64(len(corpus)) {
			out.State = "complete"
		} else if discovered == 0 && done == 0 {
			out.State = "pending"
		}
		if done == 0 {
			for _, v := range corpus {
				if v.Reason != "" && v.Reason != "interrupted" {
					out.State = "failed"
					break
				}
			}
		}
	}
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	out.VersionsTotal = n(int64(len(corpus)))
	out.VersionsComplete = n(complete)
	out.FilesTotal = n(files)
	out.FilesDiscovered = n(discovered)
	out.HTMLTotal = n(total)
	out.HTMLComplete = n(done)
	out.HTMLFailed = n(failed)
	out.HTMLPending = n(total - done - failed)
	out.UnsupportedBodies = n(unsupported)
	codes := []string{}
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		out.Reasons = append(out.Reasons, IndexReason{code, n(counts[code])})
	}
	return out
}

const searchOrderingRevision = 2

// visibleSearchState excludes document revision/title/latest and denied identities.
// The selected projection and coverage are read in the same authenticated transaction.
func visibleSearchState(l domain.Library, principal string, q search.Query, o SearchOptions, corpus []domain.SearchVersionState, coverage SearchCoverage) string {
	projection := make([]domain.SearchVersionState, len(corpus))
	for i, v := range corpus {
		if v.Reason != "" {
			v.Reason = safeIndexReason(v.Reason)
		}
		codes := map[string]bool{}
		for _, r := range v.UnitReasons {
			codes[safeIndexReason(r)] = true
		}
		v.UnitReasons = []string{}
		for code := range codes {
			v.UnitReasons = append(v.UnitReasons, code)
		}
		sort.Strings(v.UnitReasons)
		projection[i] = v
	}
	return validator([]any{l.ID, l.RuntimeGeneration, l.AuthEpoch, l.StreamEpoch, principal, q.Clauses, o.Project, o.Lifecycle, o.Versions, o.Kind, o.Limit, domain.SearchExtractorRevision, searchOrderingRevision, projection, coverage})
}
