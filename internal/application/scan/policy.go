package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

func NormalizeDepth(depth string) (string, error) {
	d := strings.TrimSpace(depth)
	if d == "" {
		return "", nil
	}
	switch d {
	case "change_focused", "change-focused":
		return "change_focused", nil
	case "full_scope", "full-scope":
		return "full_scope", nil
	case "deep_semantic", "deep-semantic":
		return "deep_semantic", nil
	default:
		return "", domain.ErrInvalidTarget
	}
}

func ValidateAutoApplyMode(mode string) bool {
	switch mode {
	case "disabled", "propose", "auto":
		return true
	default:
		return false
	}
}

func StricterAutoApplyMode(a, b string) string {
	rank := func(m string) int {
		switch m {
		case "disabled":
			return 0
		case "propose":
			return 1
		case "auto":
			return 2
		default:
			return 0
		}
	}
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

func StricterContentGeneration(a, b string) string {
	rank := func(m string) int {
		switch m {
		case "disabled":
			return 0
		case "propose":
			return 1
		case "draft":
			return 2
		default:
			return 0
		}
	}
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

func ResolveEffectivePolicy(cfg domain.Configuration, projectIDs []string, reqDepth string) (domain.ScanPolicySnapshot, error) {
	normDepth, err := NormalizeDepth(reqDepth)
	if err != nil {
		return domain.ScanPolicySnapshot{}, err
	}
	var parsed struct {
		Maintenance struct {
			Scope                     string                     `json:"scope"`
			ProjectIDs                []string                   `json:"project_ids"`
			Depth                     string                     `json:"depth"`
			MissedRuns                string                     `json:"missed_runs"`
			MaxConcurrentAIJobs       int                        `json:"max_concurrent_ai_jobs"`
			MaxProviderAttemptsPerRun int                        `json:"max_provider_attempts_per_run"`
			MaxRunSeconds             int                        `json:"max_run_seconds"`
			AutoApply                 domain.ScanAutoApplyPolicy `json:"auto_apply"`
			ContentGeneration         string                     `json:"content_generation"`
		} `json:"maintenance"`
		Projects map[string]struct {
			Maintenance *struct {
				Depth                     *string `json:"depth"`
				MaxConcurrentAIJobs       *int    `json:"max_concurrent_ai_jobs"`
				MaxProviderAttemptsPerRun *int    `json:"max_provider_attempts_per_run"`
				MaxRunSeconds             *int    `json:"max_run_seconds"`
				AutoApply                 *struct {
					BacklinkIndexRepair *string `json:"backlink_index_repair"`
					RelatedExplanation  *string `json:"related_explanation"`
					TopicAssignment     *string `json:"topic_assignment"`
					FreshnessAssessment *string `json:"freshness_assessment"`
				} `json:"auto_apply"`
				ContentGeneration *string `json:"content_generation"`
			} `json:"maintenance"`
		} `json:"projects"`
	}
	raw := cfg.Value
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return domain.ScanPolicySnapshot{}, err
	}
	out := domain.ScanPolicySnapshot{
		Scope:                     parsed.Maintenance.Scope,
		ProjectIDs:                append([]string(nil), parsed.Maintenance.ProjectIDs...),
		Depth:                     parsed.Maintenance.Depth,
		MissedRuns:                parsed.Maintenance.MissedRuns,
		MaxConcurrentAIJobs:       parsed.Maintenance.MaxConcurrentAIJobs,
		MaxProviderAttemptsPerRun: parsed.Maintenance.MaxProviderAttemptsPerRun,
		MaxRunSeconds:             parsed.Maintenance.MaxRunSeconds,
		AutoApply:                 parsed.Maintenance.AutoApply,
		ContentGeneration:         parsed.Maintenance.ContentGeneration,
	}
	if out.Scope == "" {
		out.Scope = "selected_projects"
	}
	if out.Depth == "" {
		out.Depth = "change_focused"
	}
	if out.MaxConcurrentAIJobs <= 0 {
		out.MaxConcurrentAIJobs = 1
	}
	if out.MaxProviderAttemptsPerRun <= 0 {
		out.MaxProviderAttemptsPerRun = 20
	}
	if out.MaxRunSeconds <= 0 {
		out.MaxRunSeconds = 1800
	}
	if out.AutoApply.BacklinkIndexRepair == "" {
		out.AutoApply.BacklinkIndexRepair = "auto"
	}
	if out.AutoApply.RelatedExplanation == "" {
		out.AutoApply.RelatedExplanation = "propose"
	}
	if out.AutoApply.TopicAssignment == "" {
		out.AutoApply.TopicAssignment = "propose"
	}
	if out.AutoApply.FreshnessAssessment == "" {
		out.AutoApply.FreshnessAssessment = "propose"
	}
	if out.ContentGeneration == "" {
		out.ContentGeneration = "propose"
	}
	if len(projectIDs) == 1 {
		if p, ok := parsed.Projects[projectIDs[0]]; ok && p.Maintenance != nil && p.Maintenance.Depth != nil && *p.Maintenance.Depth != "" {
			if pd, e := NormalizeDepth(*p.Maintenance.Depth); e == nil && pd != "" {
				out.Depth = pd
			}
		}
	}
	for _, pid := range projectIDs {
		p, ok := parsed.Projects[pid]
		if !ok || p.Maintenance == nil {
			continue
		}
		if p.Maintenance.MaxConcurrentAIJobs != nil && *p.Maintenance.MaxConcurrentAIJobs > 0 && *p.Maintenance.MaxConcurrentAIJobs < out.MaxConcurrentAIJobs {
			out.MaxConcurrentAIJobs = *p.Maintenance.MaxConcurrentAIJobs
		}
		if p.Maintenance.MaxProviderAttemptsPerRun != nil && *p.Maintenance.MaxProviderAttemptsPerRun > 0 && *p.Maintenance.MaxProviderAttemptsPerRun < out.MaxProviderAttemptsPerRun {
			out.MaxProviderAttemptsPerRun = *p.Maintenance.MaxProviderAttemptsPerRun
		}
		if p.Maintenance.MaxRunSeconds != nil && *p.Maintenance.MaxRunSeconds > 0 && *p.Maintenance.MaxRunSeconds < out.MaxRunSeconds {
			out.MaxRunSeconds = *p.Maintenance.MaxRunSeconds
		}
		if p.Maintenance.AutoApply != nil {
			aa := p.Maintenance.AutoApply
			if aa.BacklinkIndexRepair != nil && *aa.BacklinkIndexRepair != "" {
				out.AutoApply.BacklinkIndexRepair = StricterAutoApplyMode(out.AutoApply.BacklinkIndexRepair, *aa.BacklinkIndexRepair)
			}
			if aa.RelatedExplanation != nil && *aa.RelatedExplanation != "" {
				out.AutoApply.RelatedExplanation = StricterAutoApplyMode(out.AutoApply.RelatedExplanation, *aa.RelatedExplanation)
			}
			if aa.TopicAssignment != nil && *aa.TopicAssignment != "" {
				out.AutoApply.TopicAssignment = StricterAutoApplyMode(out.AutoApply.TopicAssignment, *aa.TopicAssignment)
			}
			if aa.FreshnessAssessment != nil && *aa.FreshnessAssessment != "" {
				out.AutoApply.FreshnessAssessment = StricterAutoApplyMode(out.AutoApply.FreshnessAssessment, *aa.FreshnessAssessment)
			}
		}
		if p.Maintenance.ContentGeneration != nil && *p.Maintenance.ContentGeneration != "" {
			out.ContentGeneration = StricterContentGeneration(out.ContentGeneration, *p.Maintenance.ContentGeneration)
		}
	}
	if normDepth != "" {
		out.Depth = normDepth
	}
	return out, nil
}

func StricterPositiveInt(captured, current int) int {
	if captured <= 0 {
		return current
	}
	if current <= 0 {
		return captured
	}
	if current < captured {
		return current
	}
	return captured
}

func EffectiveMaxRunSeconds(captured int, cfg domain.Configuration, projectIDs []string) int {
	cur, err := ResolveEffectivePolicy(cfg, projectIDs, "")
	if err != nil {
		return captured
	}
	return StricterPositiveInt(captured, cur.MaxRunSeconds)
}

func ComputeScopeHash(libraryID string, scope domain.ScanRequestedScope, versions []domain.ScanCapturedVersion, configRev int64, perms map[string]int64, policy domain.ScanPolicySnapshot) string {
	return ComputeAuthorizedScopeHash(domain.Session{}, libraryID, scope, versions, configRev, perms, policy)
}

func ComputeAuthorizedScopeHash(actor domain.Session, libraryID string, scope domain.ScanRequestedScope, versions []domain.ScanCapturedVersion, configRev int64, perms map[string]int64, policy domain.ScanPolicySnapshot) string {
	type semanticTargetKey struct {
		ProjectID           string `json:"project_id"`
		SourceID            string `json:"source_id"`
		Kind                string `json:"kind"`
		Mode                string `json:"mode"`
		SelectionSource     string `json:"selection_source"`
		RequestedRevision   string `json:"requested_revision,omitempty"`
		RequestedBranch     string `json:"requested_branch,omitempty"`
		RequestedSnapshotID string `json:"requested_snapshot_id,omitempty"`
		ResolvedRevision    string `json:"resolved_revision,omitempty"`
		SnapshotSHA256      string `json:"snapshot_sha256,omitempty"`
		AssetPath           string `json:"asset_path,omitempty"`
		BytesOrigin         string `json:"bytes_origin,omitempty"`
		Availability        string `json:"availability"`
		Reason              string `json:"reason,omitempty"`
		ReassessmentNeeded  bool   `json:"reassessment_required,omitempty"`
	}
	type semanticSelectionKey struct {
		ProjectID   string   `json:"project_id,omitempty"`
		SourceID    string   `json:"source_id,omitempty"`
		SourceIDs   []string `json:"source_ids,omitempty"`
		Mode        string   `json:"mode,omitempty"`
		Revision    string   `json:"revision,omitempty"`
		Branch      string   `json:"branch,omitempty"`
		SnapshotID  string   `json:"snapshot_id,omitempty"`
		TargetSetID string   `json:"target_set_id,omitempty"`
	}
	type versionKey struct {
		Ordinal             int                  `json:"ordinal"`
		ProjectID           string               `json:"project_id"`
		DocumentID          string               `json:"document_id"`
		VersionID           string               `json:"version_id"`
		ManifestSHA256      string               `json:"manifest_sha256"`
		MetadataSHA256      string               `json:"metadata_sha256"`
		SelectedMode        string               `json:"selected_mode"`
		SelectionSource     string               `json:"selection_source"`
		ExplicitTargetSetID string               `json:"explicit_target_set_id,omitempty"`
		Selection           semanticSelectionKey `json:"selection"`
		Targets             []semanticTargetKey  `json:"targets"`
		Exclusion           string               `json:"exclusion"`
	}
	vkeys := make([]versionKey, 0, len(versions))
	for _, v := range versions {
		var sel domain.ScanSourceSelector
		_ = json.Unmarshal(v.SelectionJSON, &sel)
		explicitTS := ""
		if v.SelectionSource == "target_set" {
			explicitTS = v.TargetSetID
		}
		sids := append([]string(nil), sel.SourceIDs...)
		sort.Strings(sids)
		selKey := semanticSelectionKey{
			ProjectID:   sel.ProjectID,
			SourceID:    sel.SourceID,
			SourceIDs:   sids,
			Mode:        sel.Mode,
			Revision:    sel.Revision,
			Branch:      sel.Branch,
			SnapshotID:  sel.SnapshotID,
			TargetSetID: explicitTS,
		}
		tkeys := make([]semanticTargetKey, 0, len(v.Targets))
		for _, t := range v.Targets {
			tkeys = append(tkeys, semanticTargetKey{
				ProjectID:           t.ProjectID,
				SourceID:            t.SourceID,
				Kind:                t.Kind,
				Mode:                t.Mode,
				SelectionSource:     t.SelectionSource,
				RequestedRevision:   t.RequestedSelection.Revision,
				RequestedBranch:     t.RequestedSelection.Branch,
				RequestedSnapshotID: t.RequestedSelection.SnapshotID,
				ResolvedRevision:    t.ResolvedRevision,
				SnapshotSHA256:      t.SnapshotSHA256,
				AssetPath:           t.AssetPath,
				BytesOrigin:         t.BytesOrigin,
				Availability:        t.Availability,
				Reason:              t.Reason,
				ReassessmentNeeded:  t.ReassessmentNeeded,
			})
		}
		vkeys = append(vkeys, versionKey{
			Ordinal:             v.VersionOrdinal,
			ProjectID:           v.ProjectID,
			DocumentID:          v.DocumentID,
			VersionID:           v.VersionID,
			ManifestSHA256:      v.ManifestSHA256,
			MetadataSHA256:      v.MetadataSHA256,
			SelectedMode:        v.SelectedMode,
			SelectionSource:     v.SelectionSource,
			ExplicitTargetSetID: explicitTS,
			Selection:           selKey,
			Targets:             tkeys,
			Exclusion:           v.ExclusionReason,
		})
	}
	type permPair struct {
		ProjectID string `json:"project_id"`
		Revision  int64  `json:"revision"`
	}
	pp := make([]permPair, 0, len(perms))
	for k, v := range perms {
		pp = append(pp, permPair{ProjectID: k, Revision: v})
	}
	sort.Slice(pp, func(i, j int) bool { return pp[i].ProjectID < pp[j].ProjectID })

	normScope := scope
	normScope.PagePath = normScope.PathPrefix
	if len(normScope.Sources) > 0 {
		normSources := make([]domain.ScanSourceSelector, len(normScope.Sources))
		for i, s := range normScope.Sources {
			sc := s
			sc.SourceIDs = append([]string(nil), s.SourceIDs...)
			sort.Strings(sc.SourceIDs)
			normSources[i] = sc
		}
		sort.Slice(normSources, func(i, j int) bool {
			bi, _ := json.Marshal(normSources[i])
			bj, _ := json.Marshal(normSources[j])
			return string(bi) < string(bj)
		})
		normScope.Sources = normSources
	}

	type actorKey struct {
		PrincipalID string   `json:"principal_id,omitempty"`
		AuthKind    string   `json:"auth_kind,omitempty"`
		Actions     []string `json:"actions,omitempty"`
		ProjectIDs  []string `json:"project_ids,omitempty"`
		VersionIDs  []string `json:"version_ids,omitempty"`
	}
	var ak *actorKey
	if actor.PrincipalID != "" || actor.Scope.Kind != "" {
		ak = &actorKey{
			PrincipalID: actor.PrincipalID,
			AuthKind:    actor.Scope.Kind,
		}
		if actor.Scope.Kind != "owner" {
			actions := append([]string(nil), actor.Scope.Actions...)
			sort.Strings(actions)
			pids := append([]string(nil), actor.Scope.ProjectIDs...)
			sort.Strings(pids)
			vids := append([]string(nil), actor.Scope.VersionIDs...)
			sort.Strings(vids)
			ak.Actions = actions
			ak.ProjectIDs = pids
			ak.VersionIDs = vids
		}
	}

	payload := struct {
		Actor       *actorKey                 `json:"actor,omitempty"`
		LibraryID   string                    `json:"library_id"`
		Scope       domain.ScanRequestedScope `json:"scope"`
		Versions    []versionKey              `json:"versions"`
		ConfigRev   int64                     `json:"config_revision"`
		Permissions []permPair                `json:"permissions"`
		Policy      domain.ScanPolicySnapshot `json:"policy"`
	}{
		Actor:       ak,
		LibraryID:   libraryID,
		Scope:       normScope,
		Versions:    vkeys,
		ConfigRev:   configRev,
		Permissions: pp,
		Policy:      policy,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ComputeEvidenceFingerprint(kind, versionID string, payload any) string {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256([]byte(kind + ":" + versionID + ":" + string(raw)))
	return hex.EncodeToString(sum[:])
}
