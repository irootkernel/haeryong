package foundation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

func exportHash(hash string) bool {
	if len(hash) != 64 || hash != strings.ToLower(hash) {
		return false
	}
	_, e := hex.DecodeString(hash)
	return e == nil
}
func validExportVersion(v domain.CopyVersion) bool {
	if !copyUUID.MatchString(v.VersionID) || !copyUUID.MatchString(v.DocumentID) || !exportHash(v.PayloadSHA) || !exportHash(v.ManifestSHA) || (v.MetadataSHA != "" && !exportHash(v.MetadataSHA)) {
		return false
	}
	base := "versions/" + v.VersionID
	if v.InventoryPath != base+"/inventory.jsonl" && v.InventoryPath != "metadata/"+base+"/inventory.jsonl" {
		return false
	}
	if v.MetadataRoot == nil {
		return v.RecordsSHA == "" && v.DependenciesSHA == ""
	}
	return (*v.MetadataRoot == base || *v.MetadataRoot == "metadata/"+base) && v.InventoryPath == *v.MetadataRoot+"/inventory.jsonl" && exportHash(v.RecordsSHA) && exportHash(v.DependenciesSHA)
}
func verifyExportDependency(ctx context.Context, payload, view *exportView, v domain.CopyVersion, raw json.RawMessage, bindings *exportBindings) error {
	var kind struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &kind) != nil {
		return domain.ErrContentIntegrity
	}
	var state string
	switch kind.Kind {
	case "reference", "note_anchor":
		var row struct {
			Kind     string  `json:"kind"`
			Document string  `json:"document_id"`
			Version  string  `json:"version_id"`
			Page     *string `json:"page_path"`
			Region   *string `json:"region_id"`
			State    string  `json:"state"`
		}
		if e := decodeCopyValue(raw, &row); e != nil {
			return e
		}
		if !copyUUID.MatchString(row.Document) || !validExportAnchor(domain.AnnotationAnchor{VersionID: row.Version, PagePath: row.Page, RegionID: row.Region}) {
			return domain.ErrContentIntegrity
		}
		if row.Kind == "note_anchor" && row.Document != v.DocumentID {
			return domain.ErrContentIntegrity
		}
		state = row.State
		link := newExportLink(row.Kind, v.VersionID, row.Document, row.Version, row.Page, row.Region)
		if old, ok := bindings.dependencies[link]; ok && old != state {
			return domain.ErrContentIntegrity
		}
		bindings.dependencies[link] = state
	case "source":
		var row struct {
			Kind    string          `json:"kind"`
			ID      string          `json:"source_id"`
			Version string          `json:"version_id"`
			State   string          `json:"state"`
			Source  json.RawMessage `json:"source"`
		}
		if e := decodeCopyValue(raw, &row); e != nil {
			return e
		}
		if row.ID == "" || row.Version != v.VersionID || !json.Valid(row.Source) {
			return domain.ErrContentIntegrity
		}
		if e := verifyExportSource(payload, v, row.ID, row.State, row.Source); e != nil {
			return e
		}
		if e := bindings.value("source/"+v.VersionID+"/"+row.ID, raw); e != nil {
			return e
		}
		state = row.State
	case "verification_expectation":
		var row struct {
			Kind    string  `json:"kind"`
			ID      string  `json:"verification_id"`
			Version string  `json:"version_id"`
			State   string  `json:"state"`
			Hash    string  `json:"sha256,omitempty"`
			Size    *int64  `json:"bytes,omitempty"`
			Inline  *[]byte `json:"inline_base64,omitempty"`
		}
		if e := decodeCopyValue(raw, &row); e != nil {
			return e
		}
		if !copyUUID.MatchString(row.ID) || row.Version != v.VersionID {
			return domain.ErrContentIntegrity
		}
		if row.Hash != "" && !exportHash(row.Hash) {
			return domain.ErrContentIntegrity
		}
		if e := bindings.value("expectation/"+v.VersionID+"/"+row.ID, raw); e != nil {
			return e
		}
		state = row.State
		if state == "included" {
			if row.Size == nil || *row.Size < 0 || !exportHash(row.Hash) {
				return domain.ErrContentIntegrity
			}
			if row.Inline != nil {
				h := sha256.Sum256(*row.Inline)
				if hex.EncodeToString(h[:]) != row.Hash || int64(len(*row.Inline)) != *row.Size {
					return domain.ErrContentIntegrity
				}
			} else if e := verifyMetadataObject(ctx, view, *v.MetadataRoot, domain.Object{Hash: row.Hash, Size: *row.Size}); e != nil {
				return e
			}
		}
	default:
		return domain.ErrContentIntegrity
	}
	switch state {
	case "included", "external_reference", "tombstoned", "unavailable", "excluded_by_scope":
		return nil
	default:
		return domain.ErrContentIntegrity
	}
}

// These are the declared source descriptor fields, not source availability or
// authenticity proof. The original payload remains authoritative for asset
// membership; reference-only metadata never implies retained source bytes.
func verifyExportSource(payload *exportView, v domain.CopyVersion, id, state string, raw json.RawMessage) error {
	var source struct {
		ID        string  `json:"id"`
		Kind      string  `json:"kind"`
		Retention string  `json:"retention_mode"`
		Label     *string `json:"release_label,omitempty"`
		Repo      *string `json:"repo_id,omitempty"`
		Format    *string `json:"object_format,omitempty"`
		Revision  *string `json:"exact_revision,omitempty"`
		Snapshot  *string `json:"snapshot_sha256,omitempty"`
		Asset     *string `json:"asset_path,omitempty"`
		Bytes     *string `json:"bytes_sha256,omitempty"`
	}
	if e := decodeCopyValue(raw, &source); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return domain.ErrContentIntegrity
		}
	}
	if source.ID != id || source.Asset != nil && domain.ValidatePayloadPath(*source.Asset) != nil || source.Bytes != nil && !exportHash(*source.Bytes) {
		return domain.ErrContentIntegrity
	}
	switch source.Kind {
	case "git":
		if source.Repo == nil || !copyUUID.MatchString(*source.Repo) || source.Format == nil || source.Revision == nil || source.Snapshot != nil {
			return domain.ErrContentIntegrity
		}
		length := 40
		if *source.Format == "sha256" {
			length = 64
		} else if *source.Format != "sha1" {
			return domain.ErrContentIntegrity
		}
		if len(*source.Revision) != length || *source.Revision != strings.ToLower(*source.Revision) {
			return domain.ErrContentIntegrity
		}
		if _, e := hex.DecodeString(*source.Revision); e != nil {
			return domain.ErrContentIntegrity
		}
	case "snapshot":
		if source.Snapshot == nil || !exportHash(*source.Snapshot) || source.Repo != nil || source.Format != nil || source.Revision != nil {
			return domain.ErrContentIntegrity
		}
	default:
		return domain.ErrContentIntegrity
	}
	expected := "external_reference"
	switch source.Retention {
	case "reference_only":
	case "selected_snapshot":
		if source.Asset == nil || source.Bytes == nil && source.Snapshot == nil {
			return domain.ErrContentIntegrity
		}
		entry, ok := payload.payloadEntry(v.PayloadRoot, *source.Asset)
		if !ok || entry.kind != "file" {
			return domain.ErrContentIntegrity
		}
		want := source.Snapshot
		if source.Bytes != nil {
			want = source.Bytes
		}
		if entry.hash != *want {
			return domain.ErrContentIntegrity
		}
		expected = "included"

	default:
		return domain.ErrContentIntegrity
	}
	if state != expected {
		return domain.ErrContentIntegrity
	}
	return nil
}
