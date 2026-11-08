package scan

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"haeryong.local/haeryong/internal/domain"
)

var _ = It("normalizes CLI hyphenated and canonical scan depths and rejects unsupported values", Label("unit"), func() {
	Expect(NormalizeDepth("")).To(Equal(""))
	Expect(NormalizeDepth("change-focused")).To(Equal("change_focused"))
	Expect(NormalizeDepth("change_focused")).To(Equal("change_focused"))
	Expect(NormalizeDepth("full-scope")).To(Equal("full_scope"))
	Expect(NormalizeDepth("full_scope")).To(Equal("full_scope"))
	Expect(NormalizeDepth("deep-semantic")).To(Equal("deep_semantic"))
	Expect(NormalizeDepth("deep_semantic")).To(Equal("deep_semantic"))

	_, err := NormalizeDepth("quick")
	Expect(err).To(MatchError(domain.ErrInvalidTarget))
})

var _ = It("enforces the auto-apply strictness lattice and resolves multi-project policy snapshots", Label("unit"), func() {
	Expect(StricterAutoApplyMode("auto", "propose")).To(Equal("propose"))
	Expect(StricterAutoApplyMode("propose", "disabled")).To(Equal("disabled"))
	Expect(StricterAutoApplyMode("disabled", "auto")).To(Equal("disabled"))
	Expect(StricterAutoApplyMode("auto", "auto")).To(Equal("auto"))

	Expect(StricterContentGeneration("draft", "propose")).To(Equal("propose"))
	Expect(StricterContentGeneration("propose", "disabled")).To(Equal("disabled"))
	Expect(StricterContentGeneration("disabled", "draft")).To(Equal("disabled"))

	cfgJSON, err := json.Marshal(map[string]any{
		"maintenance": map[string]any{
			"scope":                         "selected_projects",
			"project_ids":                   []string{"p1", "p2"},
			"depth":                         "change_focused",
			"missed_runs":                   "coalesce_one",
			"max_concurrent_ai_jobs":        1,
			"max_provider_attempts_per_run": 8,
			"max_run_seconds":               1800,
			"auto_apply": map[string]any{
				"backlink_index_repair": "auto",
				"related_explanation":   "propose",
				"topic_assignment":      "propose",
				"freshness_assessment":  "auto",
			},
			"content_generation": "propose",
		},
		"projects": map[string]any{
			"p1": map[string]any{
				"maintenance": map[string]any{
					"max_concurrent_ai_jobs":        4,
					"max_provider_attempts_per_run": 5,
					"max_run_seconds":               300,
					"auto_apply": map[string]any{
						"backlink_index_repair": "propose",
					},
				},
			},
			"p2": map[string]any{
				"maintenance": map[string]any{
					"max_concurrent_ai_jobs":        2,
					"max_provider_attempts_per_run": 12,
					"max_run_seconds":               3600,
					"auto_apply": map[string]any{
						"backlink_index_repair": "auto",
						"freshness_assessment":  "disabled",
					},
					"content_generation": "disabled",
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())

	snap, err := ResolveEffectivePolicy(domain.Configuration{Revision: 4, Value: cfgJSON}, []string{"p1", "p2"}, "full-scope")
	Expect(err).NotTo(HaveOccurred())
	Expect(snap.Depth).To(Equal("full_scope"))
	Expect(snap.MaxConcurrentAIJobs).To(Equal(1))
	Expect(snap.MaxProviderAttemptsPerRun).To(Equal(5))
	Expect(snap.MaxRunSeconds).To(Equal(300))
	Expect(snap.AutoApply.BacklinkIndexRepair).To(Equal("propose"))
	Expect(snap.AutoApply.FreshnessAssessment).To(Equal("disabled"))
	Expect(snap.ContentGeneration).To(Equal("disabled"))

	// Runtime narrowing vs expansion on EffectiveMaxRunSeconds:
	Expect(EffectiveMaxRunSeconds(600, domain.Configuration{Revision: 4, Value: cfgJSON}, []string{"p1", "p2"})).To(Equal(300))
	Expect(EffectiveMaxRunSeconds(120, domain.Configuration{Revision: 4, Value: cfgJSON}, []string{"p1", "p2"})).To(Equal(120))
})

var _ = It("resolves four-tier source-selection precedence (EE-P01) deterministically", Label("unit"), func() {
	hist := "historical"
	moving := "moving_target"

	cfgCurrentJSON, err := json.Marshal(map[string]any{
		"projects": map[string]any{
			"p1": map[string]any{
				"source_mode_default": "current",
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	cfgCurrent := domain.Configuration{Revision: 1, Value: cfgCurrentJSON}
	cfgEmpty := domain.Configuration{Revision: 1, Value: json.RawMessage(`{}`)}

	// Tier 1a: explicit target_set_id binds that exact committed capture
	mode, src, tsID, _, err := ResolveVersionSourceMode(
		cfgCurrent, "p1", &hist,
		domain.ScanRequest{TargetSetID: "ts-123"},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(mode).To(Equal(""))
	Expect(src).To(Equal("target_set"))
	Expect(tsID).To(Equal("ts-123"))

	// Tier 1b: explicit source_mode overrides historical_intent and project_default
	mode, src, tsID, _, err = ResolveVersionSourceMode(
		cfgEmpty, "p1", &hist,
		domain.ScanRequest{SourceMode: "current"},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(mode).To(Equal("current"))
	Expect(src).To(Equal("request_override"))
	Expect(tsID).To(Equal(""))

	// Tier 2: historical source_intent forces pinned even when project_default is current
	mode, src, tsID, _, err = ResolveVersionSourceMode(
		cfgCurrent, "p1", &hist,
		domain.ScanRequest{},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(mode).To(Equal("pinned"))
	Expect(src).To(Equal("historical_intent"))
	Expect(tsID).To(Equal(""))

	// Tier 3: moving_target or nil source_intent uses project_default when configured
	mode, src, _, _, err = ResolveVersionSourceMode(
		cfgCurrent, "p1", &moving,
		domain.ScanRequest{},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(mode).To(Equal("current"))
	Expect(src).To(Equal("project_default"))

	// Tier 4: moving_target without project_default does not force current; defaults to pinned
	mode, src, _, _, err = ResolveVersionSourceMode(
		cfgEmpty, "p1", &moving,
		domain.ScanRequest{},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(mode).To(Equal("pinned"))
	Expect(src).To(Equal("default_pinned"))
})

var _ = It("computes canonical scope_hash and evidence_fingerprint across equivalent and distinct source bindings", Label("unit"), func() {
	policy := domain.ScanPolicySnapshot{
		Scope:      "selected_projects",
		ProjectIDs: []string{"p1"},
		Depth:      "change_focused",
		AutoApply:  domain.ScanAutoApplyPolicy{BacklinkIndexRepair: "auto"},
	}
	perms := map[string]int64{"p1": 2}
	rosterA := []domain.ScanCapturedVersion{
		{
			VersionOrdinal:  0,
			ProjectID:       "p1",
			DocumentID:      "d1",
			VersionID:       "v1",
			ManifestSHA256:  "m1",
			SelectedMode:    "pinned",
			SelectionSource: "version_metadata",
			Targets: []domain.ScanSourceTarget{
				{
					ProjectID:        "p1",
					SourceID:         "repo-main",
					Kind:             "git",
					Mode:             "pinned",
					SelectionSource:  "version_metadata",
					ResolvedRevision: "abc1234",
					Availability:     "available",
				},
			},
		},
	}

	scopeA := domain.ScanRequestedScope{
		Scope:      "selected_projects",
		ProjectIDs: []string{"p1"},
		PathPrefix: "index.html",
		Depth:      "change_focused",
		Sources:    []domain.ScanSourceSelector{{ProjectID: "p1", SourceIDs: []string{"s2", "s1"}}},
	}
	scopeB := domain.ScanRequestedScope{
		Scope:      "selected_projects",
		ProjectIDs: []string{"p1"},
		PathPrefix: "index.html",
		PagePath:   "index.html",
		Depth:      "change_focused",
		Sources:    []domain.ScanSourceSelector{{ProjectID: "p1", SourceIDs: []string{"s1", "s2"}}},
	}
	hashA := ComputeScopeHash("lib-1", scopeA, rosterA, 1, perms, policy)
	hashB := ComputeScopeHash("lib-1", scopeB, rosterA, 1, perms, policy)
	Expect(hashA).To(Equal(hashB))

	// Changing resolved revision or selection source produces a distinct scope_hash
	rosterMoved := []domain.ScanCapturedVersion{
		{
			VersionOrdinal:  0,
			ProjectID:       "p1",
			DocumentID:      "d1",
			VersionID:       "v1",
			ManifestSHA256:  "m1",
			SelectedMode:    "current",
			SelectionSource: "request_explicit",
			Targets: []domain.ScanSourceTarget{
				{
					ProjectID:        "p1",
					SourceID:         "repo-main",
					Kind:             "git",
					Mode:             "current",
					SelectionSource:  "request_explicit",
					ResolvedRevision: "def5678",
					Availability:     "available",
				},
			},
		},
	}
	hashMoved := ComputeScopeHash("lib-1", scopeA, rosterMoved, 1, perms, policy)
	Expect(hashMoved).NotTo(Equal(hashA))

	fp1 := ComputeEvidenceFingerprint("backlink_index_repair", "v1", map[string]any{"missing": "1"})
	fp2 := ComputeEvidenceFingerprint("backlink_index_repair", "v1", map[string]any{"missing": "1"})
	fp3 := ComputeEvidenceFingerprint("backlink_index_repair", "v1", map[string]any{"missing": "2"})
	Expect(fp1).To(Equal(fp2))
	Expect(fp3).NotTo(Equal(fp1))

	// Multiple Sources[] entries in different order produce identical scope_hash, while differing per-source requested selections do not coalesce.
	scopeMulti1 := domain.ScanRequestedScope{
		Scope:      "selected_projects",
		ProjectIDs: []string{"p1"},
		Depth:      "change_focused",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: "p1", SourceID: "s2", Mode: "current", Branch: "main"},
			{ProjectID: "p1", SourceID: "s1", Mode: "pinned", Revision: "aaa111"},
		},
	}
	scopeMulti2 := domain.ScanRequestedScope{
		Scope:      "selected_projects",
		ProjectIDs: []string{"p1"},
		Depth:      "change_focused",
		Sources: []domain.ScanSourceSelector{
			{ProjectID: "p1", SourceID: "s1", Mode: "pinned", Revision: "aaa111"},
			{ProjectID: "p1", SourceID: "s2", Mode: "current", Branch: "main"},
		},
	}
	Expect(ComputeScopeHash("lib-1", scopeMulti1, rosterA, 1, perms, policy)).To(
		Equal(ComputeScopeHash("lib-1", scopeMulti2, rosterA, 1, perms, policy)),
	)

	// Validate selector field rules and multi-file composite digest sensitivity to second file mutation.
	Expect(validateSelectorFields(domain.ScanSourceSelector{Mode: "pinned", Branch: "main"}, "", "")).To(MatchError(domain.ErrInvalidTarget))
	Expect(validateSelectorFields(domain.ScanSourceSelector{Revision: "abc", Branch: "main"}, "", "")).To(MatchError(domain.ErrInvalidTarget))
	Expect(validateSelectorFields(domain.ScanSourceSelector{TargetSetID: "ts1", Revision: "abc"}, "", "")).To(MatchError(domain.ErrInvalidTarget))
	Expect(validateSelectorFields(domain.ScanSourceSelector{SourceID: "s1", SourceIDs: []string{"s2"}}, "", "")).To(MatchError(domain.ErrInvalidTarget))

	compA := compositeReadDigestsSHA([]readPathDigest{
		{ReadID: "r1", Path: "a.txt", Hash: "1111111111111111111111111111111111111111111111111111111111111111"},
		{ReadID: "r2", Path: "b.txt", Hash: "2222222222222222222222222222222222222222222222222222222222222222"},
	})
	compB := compositeReadDigestsSHA([]readPathDigest{
		{ReadID: "r1", Path: "a.txt", Hash: "1111111111111111111111111111111111111111111111111111111111111111"},
		{ReadID: "r2", Path: "b.txt", Hash: "3333333333333333333333333333333333333333333333333333333333333333"},
	})
	Expect(compA).NotTo(BeEmpty())
	Expect(compB).NotTo(Equal(compA))

	// Authorized scope_hash separates distinct principals and owner vs workflow while coalescing same-principal equivalent scope.
	wf1 := domain.Session{
		PrincipalID: "wp-1",
		Scope: domain.CredentialScope{
			Kind:       "workflow",
			Actions:    []string{"scan.request", "job.read", "version.read"},
			ProjectIDs: []string{"p1"},
			VersionIDs: []string{"v1"},
		},
	}
	wf1Reordered := domain.Session{
		PrincipalID: "wp-1",
		Scope: domain.CredentialScope{
			Kind:       "workflow",
			Actions:    []string{"version.read", "scan.request", "job.read"},
			ProjectIDs: []string{"p1"},
			VersionIDs: []string{"v1"},
		},
	}
	wf2 := domain.Session{
		PrincipalID: "wp-2",
		Scope: domain.CredentialScope{
			Kind:       "workflow",
			Actions:    []string{"scan.request", "job.read", "version.read"},
			ProjectIDs: []string{"p1"},
			VersionIDs: []string{"v1"},
		},
	}
	ownerActor := domain.Session{
		PrincipalID: "owner-1",
		Scope:       domain.CredentialScope{Kind: "owner"},
	}
	authHash1 := ComputeAuthorizedScopeHash(wf1, "lib-1", scopeA, rosterA, 1, perms, policy)
	authHash1b := ComputeAuthorizedScopeHash(wf1Reordered, "lib-1", scopeB, rosterA, 1, perms, policy)
	authHash2 := ComputeAuthorizedScopeHash(wf2, "lib-1", scopeA, rosterA, 1, perms, policy)
	authHashOwner := ComputeAuthorizedScopeHash(ownerActor, "lib-1", scopeA, rosterA, 1, perms, policy)
	Expect(authHash1).To(Equal(authHash1b))
	Expect(authHash1).NotTo(Equal(authHash2))
	Expect(authHash1).NotTo(Equal(authHashOwner))
})
