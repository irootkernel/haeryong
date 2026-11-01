package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"path/filepath"
	"reflect"
)

func (t *writeTx) validateGenerationTransport(a domain.ProviderAttempt, v domain.ProviderTransport) error {
	if v.Generation == nil {
		if v.Sandbox != "read-only" {
			return domain.ErrInvalidTarget
		}
		return nil
	}
	g := v.Generation
	j, e := t.Job(a.JobID)
	if e != nil {
		return e
	}
	var scope domain.ProviderScope
	if j.Kind != "generation" || json.Unmarshal(j.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Generation == nil {
		return domain.ErrInvalidTarget
	}
	expected := scope.Invocation.Generation
	if !reflect.DeepEqual(g.References, expected.References) || g.ReferencesAcknowledged && (len(g.References) == 0 || v.AcceptedAt == "") {
		return domain.ErrInvalidTarget
	}
	if !reflect.DeepEqual(g.Prompt, scope.Invocation.PromptRef) || g.PromptInputAcknowledged && (g.Prompt == nil || v.AcceptedAt == "") {
		return domain.ErrInvalidTarget
	}
	if g.Format != "directory_zip" || v.Sandbox != "workspace-write" || !filepath.IsAbs(v.CWD) || g.SkillSHA != expected.SkillSHA || g.SkillVersion != expected.SkillVersion || g.AdmittedSkillPath != expected.SkillPath || g.NativeSkillPath != filepath.Join(v.CWD, ".agents", "skills", "use-haeryong", "SKILL.md") || g.OutputDirectory != filepath.Join(v.CWD, "result") || !g.SkillDiscovered {
		return domain.ErrInvalidTarget
	}
	if g.SkillInputAcknowledged && v.AcceptedAt == "" || g.PublicFinalSHA != "" && (len(g.PublicFinalSHA) != 64 || !g.SkillInputAcknowledged) || g.CapturedAt != "" && (g.PublicFinalSHA == "" || !v.LocalClosed || v.CleanupState != "drained") || g.PublicFinalSize < 0 {
		return domain.ErrInvalidTarget
	}
	return nil
}

// Generation observations merge monotonically alongside existing transport
// facts. Missing/disagreeing identity is retained as conflict, not synthesized.
func mergeGenerationTransport(a, b *domain.GenerationTransport) (*domain.GenerationTransport, bool) {
	if a == nil && b == nil {
		return nil, false
	}
	if a == nil || b == nil {
		return b, true
	}
	before, after := *a, *b
	clear := func(g *domain.GenerationTransport) {
		g.SkillInputAcknowledged = false
		g.PromptInputAcknowledged = false
		g.ReferencesAcknowledged = false
		g.PublicFinalSHA = ""
		g.PublicFinalSize = 0
		g.CapturedAt = ""
	}
	clear(&before)
	clear(&after)
	if !reflect.DeepEqual(before, after) {
		return b, true
	}
	result := *a
	result.SkillInputAcknowledged = a.SkillInputAcknowledged || b.SkillInputAcknowledged
	result.PromptInputAcknowledged = a.PromptInputAcknowledged || b.PromptInputAcknowledged
	result.ReferencesAcknowledged = a.ReferencesAcknowledged || b.ReferencesAcknowledged
	if a.PublicFinalSHA != "" && b.PublicFinalSHA != "" && (a.PublicFinalSHA != b.PublicFinalSHA || a.PublicFinalSize != b.PublicFinalSize) {
		return b, true
	}
	if result.PublicFinalSHA == "" {
		result.PublicFinalSHA, result.PublicFinalSize = b.PublicFinalSHA, b.PublicFinalSize
	}
	if result.CapturedAt == "" {
		result.CapturedAt = b.CapturedAt
	}
	return &result, false
}

// Exact request objects share the existing execution-input lifetime without
// becoming project-source capture or observation records.
func generationTransportObjects(g *domain.GenerationTransport) []domain.ProviderText {
	objects := []domain.ProviderText{}
	if g == nil {
		return objects
	}
	if g.Prompt != nil {
		objects = append(objects, *g.Prompt)
	}
	for _, ref := range g.References {
		objects = append(objects, domain.ProviderText{Hash: ref.ObjectHash, Size: ref.Size})
	}
	return objects
}
