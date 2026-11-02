package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"haeryong.local/haeryong/internal/adapters/content/staging"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

var generationAttemptID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (d *Driver) generationCWD(a domain.ProviderAttempt) (string, error) {
	return d.attemptCWD(a, "generation-")
}

func (d *Driver) attemptCWD(a domain.ProviderAttempt, prefix string) (string, error) {
	if !generationAttemptID.MatchString(a.ID) || !filepath.IsAbs(d.Scratch) {
		return "", domain.ErrInvalidTarget
	}
	physical, e := filepath.EvalSymlinks(d.Scratch)
	if e != nil || physical != d.Scratch {
		return "", domain.ErrInvalidTarget
	}
	info, e := os.Lstat(d.Scratch)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", domain.ErrInvalidTarget
	}
	return filepath.Join(d.Scratch, prefix+a.ID), nil
}
func readGenerationSkill(path, expected string) ([]byte, error) {
	if !filepath.IsAbs(path) || len(expected) != 64 {
		return nil, domain.ErrInvalidTarget
	}
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, domain.ErrInvalidTarget
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	actual, e := file.Stat()
	if e != nil || !os.SameFile(info, actual) {
		return nil, domain.ErrContentIntegrity
	}
	data, e := io.ReadAll(file)
	if e != nil {
		return nil, e
	}
	after, e := os.Lstat(path)
	if e != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, domain.ErrContentIntegrity
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != expected {
		return nil, domain.ErrContentIntegrity
	}
	return data, nil
}
func (d *Driver) prepareGeneration(a domain.ProviderAttempt, inv *domain.ProviderInvocation) (*domain.GenerationTransport, error) {
	if inv.Generation == nil {
		return nil, nil
	}
	g := inv.Generation
	if g.SkillVersion == "" || (g.Intent != "publish" && g.Intent != "draft") {
		return nil, domain.ErrInvalidTarget
	}
	data, e := readGenerationSkill(g.SkillPath, g.SkillSHA)
	if e != nil {
		return nil, e
	}
	cwd, e := d.generationCWD(a)
	if e != nil {
		return nil, e
	}
	// An actual attempt gets a fresh destination. Never merge a prior partial
	// directory into a new attempt or retarget a restored profile's old path.
	if e = os.Mkdir(cwd, 0700); e != nil {
		return nil, e
	}
	result := filepath.Join(cwd, "result")
	if e = os.Mkdir(result, 0700); e != nil {
		return nil, e
	}
	skillDirectory := filepath.Join(cwd, ".agents", "skills", "use-haeryong")
	if e = os.MkdirAll(skillDirectory, 0700); e != nil {
		return nil, e
	}
	skillPath := filepath.Join(skillDirectory, "SKILL.md")
	file, e := os.OpenFile(skillPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	_, writeErr := file.Write(data)
	e = errors.Join(writeErr, file.Sync(), file.Close())
	if e != nil {
		return nil, e
	}
	managed := *g
	managed.SkillPath = skillPath
	inv.Generation = &managed
	inv.CWD = cwd
	return &domain.GenerationTransport{Prompt: inv.PromptRef, References: inv.Generation.References, Format: "directory_zip", SkillVersion: g.SkillVersion, SkillSHA: g.SkillSHA, AdmittedSkillPath: g.SkillPath, NativeSkillPath: skillPath, OutputDirectory: result}, nil
}
func discoverGenerationSkill(ctx context.Context, t *transport, cwd string, g *domain.GenerationTransport) error {
	if g == nil {
		return nil
	}
	var result struct {
		Data []struct {
			CWD    string `json:"cwd"`
			Skills []struct {
				Name, Path string
				Enabled    bool
			} `json:"skills"`
		} `json:"data"`
	}
	if e := t.call(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}, &result); e != nil {
		return e
	}
	for _, entry := range result.Data {
		if entry.CWD != cwd {
			continue
		}
		for _, skill := range entry.Skills {
			if skill.Name == "use-haeryong" && skill.Path == g.NativeSkillPath && skill.Enabled {
				g.SkillDiscovered = true
				return nil
			}
		}
	}
	return domain.ErrDenied
}

// captureGeneration runs only after positive owned native process/reader drain.
// The native public-final bytes and the service-produced ZIP have distinct
// identities; only the latter is consumed as a complete ordinary directory.
func (d *Driver) captureGeneration(ctx context.Context, a domain.ProviderAttempt, out work.ProviderOutcome, g *domain.GenerationTransport, reconcile bool) (result work.ProviderOutcome, failure error) {
	if g == nil || !out.CompletionObserved || out.State != "result_saved" || out.Body == nil {
		return out, nil
	}
	original := out.Body
	out.Body = nil
	defer func() {
		if e := original.Close(); e != nil {
			result.CleanupError = errors.Join(result.CleanupError, e)
			failure = errors.Join(failure, e)
		}
	}()
	if !out.LocalClosed || out.CleanupError != nil {
		return generationUnavailable(out, errCleanup)
	}
	cwd, e := d.generationCWD(a)
	if e != nil {
		return generationUnavailable(out, e)
	}
	if g.OutputDirectory != filepath.Join(cwd, "result") || g.NativeSkillPath != filepath.Join(cwd, ".agents", "skills", "use-haeryong", "SKILL.md") || !g.SkillDiscovered || !g.SkillInputAcknowledged {
		return generationUnavailable(out, domain.ErrInvalidTarget)
	}
	if _, e = readGenerationSkill(g.NativeSkillPath, g.SkillSHA); e != nil {
		return generationUnavailable(out, e)
	}
	h := sha256.New()
	n, e := io.Copy(h, guardedReader{original, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return d.guard(ctx, a, reconcile)
	}})
	if e != nil {
		return generationUnavailable(out, e)
	}
	finalSHA := hex.EncodeToString(h.Sum(nil))
	if g.PublicFinalSHA != "" && (g.PublicFinalSHA != finalSHA || g.PublicFinalSize != n) {
		return generationUnavailable(out, domain.ErrConflict)
	}
	g.PublicFinalSHA, g.PublicFinalSize = finalSHA, n
	file, e := os.CreateTemp(d.Scratch, "provider-output-*")
	if e != nil {
		return generationUnavailable(out, e)
	}
	discard := func() { _ = file.Close(); _ = os.Remove(file.Name()) }
	archiveHash := sha256.New()
	guard := func() error { return d.guard(ctx, a, reconcile) }
	if e = staging.ArchiveDirectory(ctx, g.OutputDirectory, io.MultiWriter(file, archiveHash), guard); e != nil {
		discard()
		return generationUnavailable(out, e)
	}
	if e = file.Sync(); e != nil {
		discard()
		return generationUnavailable(out, e)
	}
	size, e := file.Seek(0, io.SeekCurrent)
	if e != nil {
		discard()
		return generationUnavailable(out, e)
	}
	digest := hex.EncodeToString(archiveHash.Sum(nil))

	if _, e = file.Seek(0, io.SeekStart); e != nil {
		discard()
		return generationUnavailable(out, e)
	}
	if g.CapturedAt == "" {
		g.CapturedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	out.Body = &directoryResultReader{resultReader: resultReader{file: file, guard: func() error { return d.guard(context.WithoutCancel(ctx), a, reconcile) }}, hash: sha256.New(), expected: digest, size: size}
	return out, nil
}

type directoryResultReader struct {
	resultReader
	hash       hash.Hash
	expected   string
	size, read int64
}

func (r *directoryResultReader) Read(p []byte) (int, error) {
	n, e := r.resultReader.Read(p)
	if n > 0 {
		r.hash.Write(p[:n])
		r.read += int64(n)
	}
	if e == io.EOF && (r.read != r.size || hex.EncodeToString(r.hash.Sum(nil)) != r.expected) {
		return n, domain.ErrContentIntegrity
	}
	return n, e
}

func generationUnavailable(out work.ProviderOutcome, cause error) (work.ProviderOutcome, error) {
	if errors.Is(cause, domain.ErrInvalidTarget) || errors.Is(cause, domain.ErrContentIntegrity) || errors.Is(cause, domain.ErrNotFound) || errors.Is(cause, os.ErrNotExist) || errors.Is(cause, importer.ErrInputChanged) || errors.Is(cause, importer.ErrPackageInvalid) || errors.Is(cause, importer.ErrHashMismatch) {
		out.CompletionObserved = true
		out.State = "failed"
		out.Reason = "result_unavailable"
		return out, cause
	}
	return completedUnavailable(out, cause)
}
