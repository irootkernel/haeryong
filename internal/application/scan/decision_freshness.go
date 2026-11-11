package scan

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/application/questions"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

// Revalidate the exact retained provider contribution, not newly computed input.
func validateSavedFreshness(tx app.WriteTx, f domain.Finding, a domain.Assessment, output domain.FreshnessResult) error {
	intents, e := tx.MaintenanceIntentsByScan(f.JobID)
	if e != nil {
		return e
	}
	var saved *domain.MaintenanceIntent
	for _, m := range intents {
		if m.Mode == domain.MaintenanceModeFreshness && m.CoverageSetID == a.CoverageSetID && m.VersionID == a.VersionID {
			copy := m
			saved = &copy
			break
		}
	}
	if saved == nil || saved.PreparedAttemptID == "" {
		return actionError(409, "OPERATION_CONFLICT", "retained_assessment_unverified")
	}
	attempt, e := tx.ProviderAttempt(saved.PreparedAttemptID)
	if e != nil {
		return e
	}
	inputs, e := tx.ProviderInputs(attempt.ID)
	if e != nil {
		return e
	}
	var prepared domain.PreparedFreshness
	if json.Unmarshal(saved.PreparedOutputJSON, &prepared) != nil || prepared.InputDigest != computeMaintenanceInputDigest(attempt.ID, attempt.ResultHash, inputs) || !freshJSONEqual(actionJSON(prepared.Output), f.Evidence) {
		return actionError(409, "OPERATION_CONFLICT", "retained_assessment_unverified")
	}
	required, e := app.AllRequiredUnits(tx, a.CoverageSetID)
	if e != nil {
		return e
	}
	if len(required) != len(output.Units) {
		return actionError(409, "OPERATION_CONFLICT", "retained_assessment_unverified")
	}
	seen := map[string]bool{}
	run, e := tx.ScanRun(f.JobID)
	if e != nil {
		return e
	}
	for _, u := range output.Units {
		p, r := "", ""
		if u.PagePath != nil {
			p = *u.PagePath
		}
		if u.RegionID != nil {
			r = *u.RegionID
		}
		k := u.Kind + "\x00" + p + "\x00" + r
		if seen[k] || strings.TrimSpace(u.Rationale) == "" || u.Citations == nil {
			return domain.ErrReconciliation
		}
		seen[k] = true
		found := false
		for _, want := range required {
			if want.Kind == u.Kind && want.PagePath == p && want.RegionID == r {
				found = true
			}
		}
		if !found {
			return domain.ErrReconciliation
		}
		if u.Outcome != "assessed" && u.Outcome != "partial" && u.Outcome != "blocked" && u.Outcome != "not_checked" {
			return domain.ErrReconciliation
		}
		if u.Outcome != "assessed" && strings.TrimSpace(u.Reason) == "" || (u.Outcome == "assessed" || u.Discrepancy != "") && len(u.Citations) == 0 {
			return domain.ErrReconciliation
		}
		for _, c := range u.Citations {
			if c.TargetSetID != a.TargetSetID {
				return domain.ErrReconciliation
			}
		}
		if e = questions.ValidateSourceCitations(tx, run.PrincipalID, a.VersionID, attempt, u.Citations); e != nil {
			return e
		}
	}
	return nil
}
