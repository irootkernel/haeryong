package sqlite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func TestCurrentMetadataExactAnchorsInventoryAndPublication(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	projectID, documentID, versionID, setID := id(t), id(t), id(t), id(t)
	paths := []string{"A.html", "a.html", "é.html", "e\u0301.html", "nested/한글.html", ".hidden/空", "empty-dir", "arbitrary.unknown", "link"}
	for i := 0; i < 300; i++ {
		paths = append(paths, fmt.Sprintf("many/%04d", i))
	}
	metadata := []byte(`{"title":"Whole result","question":"optional","references":[{"id":"任意","relation":"free text"}]}`)
	page := paths[4]
	v := domain.Version{ID: versionID, DocumentID: documentID, ContentSetID: setID, Title: "Whole result", ViewerProfile: "html_js_v1", SourceBasis: "unverified", DefaultPage: &page, Metadata: metadata}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateProject(domain.Project{ID: projectID, Name: "Project"}); err != nil {
			return err
		}
		if err := tx.CreateDocument(domain.Document{ID: documentID, ProjectID: projectID, Title: "candidate"}); err != nil {
			return err
		}
		if err := tx.CreateContentSet(setID, digest([]byte("generated inventory"))); err != nil {
			return err
		}
		for _, path := range paths {
			entry := domain.Entry{ContentSetID: setID, Path: path, Kind: "directory"}
			if path == "link" {
				entry.Kind = "symlink"
				entry.LinkText = "../unchanged-target"
			}
			if err := tx.PutEntry(entry); err != nil {
				return err
			}
		}
		if err := tx.PutVersion(v); err != nil {
			return err
		}
		for _, path := range []string{paths[0], page} {
			if err := tx.PutPage(domain.Page{VersionID: versionID, Path: path}); err != nil {
				return err
			}
			if err := tx.PutRegion(domain.Region{VersionID: versionID, PagePath: path, ID: "同じ", Metadata: []byte(`{"summary":"optional"}`)}); err != nil {
				return err
			}
		}
		if err := tx.PutSource(domain.Source{VersionID: versionID, ID: "source-local", Metadata: []byte(`{"kind":"git","exact_revision":"preserved"}`)}); err != nil {
			return err
		}
		if err := tx.PutEvidence(domain.Evidence{VersionID: versionID, ID: "evidence-local", SourceID: "source-local", Metadata: []byte(`{"capture_status":"captured","path":"exact"}`)}); err != nil {
			return err
		}
		return tx.LinkRegionEvidence(versionID, page, "同じ", "evidence-local")
	}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []func(app.WriteTx) error{
		func(tx app.WriteTx) error {
			return tx.PutRegion(domain.Region{VersionID: versionID, PagePath: "missing-page", ID: "同じ"})
		},
		func(tx app.WriteTx) error {
			return tx.PutEvidence(domain.Evidence{VersionID: versionID, ID: "wrong-source", SourceID: "foreign-source"})
		},
		func(tx app.WriteTx) error {
			return tx.LinkRegionEvidence(versionID, paths[0], "missing-region", "evidence-local")
		},
	} {
		if err := s.Write(ctx, bad); err == nil {
			t.Fatal("exact same-version/page FK was not enforced")
		}
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.PublishVersion(versionID, 1) }); !errors.Is(err, domain.ErrTransition) {
		t.Fatalf("unsealed set published: %v", err)
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.SealContentSet(setID); err != nil {
			return err
		}
		return tx.PublishVersion(versionID, 1)
	}); err != nil {
		t.Fatal(err)
	}
	for _, write := range []func(app.WriteTx) error{
		func(tx app.WriteTx) error { return tx.PutPage(domain.Page{VersionID: versionID, Path: "late.html"}) },
		func(tx app.WriteTx) error {
			return tx.PutEntry(domain.Entry{ContentSetID: setID, Path: "late", Kind: "directory"})
		},
	} {
		if err := s.Write(ctx, write); !errors.Is(err, domain.ErrTransition) {
			t.Fatal("immutable published metadata changed", err)
		}
	}
	s.Close()
	s = openStore(t, writer, nil)
	stored, err := s.Version(ctx, versionID)
	if err != nil || stored.Sequence != 1 || !bytes.Equal(stored.Metadata, metadata) || *stored.DefaultPage != page {
		t.Fatalf("version roundtrip: %v %+v", err, stored)
	}
	document, err := s.Document(ctx, documentID)
	if err != nil || document.LatestVersionID != versionID || document.Title != v.Title || document.Revision != 2 {
		t.Fatalf("publication pointer/title/revision: %v %+v", err, document)
	}
	for _, path := range []string{paths[0], page} {
		if _, err := s.Region(ctx, versionID, path, "同じ"); err != nil {
			t.Fatal("page-local region slug collision", err)
		}
	}
	evidence, err := s.Evidence(ctx, versionID, "evidence-local")
	if err != nil || evidence.SourceID != "source-local" {
		t.Fatal("evidence/source roundtrip", err)
	}
	received := []string{}
	after := ""
	calls := 0
	for {
		entries, err := s.Entries(ctx, setID, after, 17)
		if err != nil {
			t.Fatal(err)
		}
		calls++
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			received = append(received, entry.Path)
			if entry.Kind == "symlink" && entry.LinkText != "../unchanged-target" {
				t.Fatal("symlink text rewritten")
			}
		}
		after = entries[len(entries)-1].Path
	}
	sort.Strings(paths)
	if !reflect.DeepEqual(paths, received) || calls < 3 {
		t.Fatalf("multi-batch UTF8/case inventory omitted/normalized paths: got=%d want=%d batches=%d", len(received), len(paths), calls)
	}
	h, _ := s.Health(ctx)
	events, _ := s.Events(ctx, h.Library.StreamEpoch, 0, 20)
	if len(events) != 1 || events[0].Revision != 2 {
		t.Fatal("publication event/revision missing")
	}
	config, err := s.Configuration(ctx)
	if err != nil || config.Revision != 1 || !json.Valid(config.Value) || !bytes.Equal(config.Value, app.DefaultConfiguration()) {
		t.Fatal("bootstrap frozen defaults lost", err)
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		return tx.SetConfiguration(domain.Configuration{Value: []byte(`{"fixture":"validated elsewhere"}`), Actor: "fixture"}, 1)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		return tx.SetConfiguration(domain.Configuration{Value: []byte(`{}`), Actor: "fixture"}, 1)
	}); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatal("configuration CAS not enforced", err)
	}
}
