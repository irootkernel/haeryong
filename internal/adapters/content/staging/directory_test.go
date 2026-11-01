package staging

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("GDIR01 captures a complete ordinary output tree through the real ZIP consumer", Label("unit"), func() {
	root := GinkgoT().TempDir()
	output := filepath.Join(root, "output")
	Expect(os.MkdirAll(filepath.Join(output, "assets", "empty"), 0700)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(output, "자료"), 0700)).To(Succeed())
	files := map[string][]byte{
		"overview.html":     []byte(`<a href="자료/detail.HTML">detail</a><script src="assets/app.js"></script>`),
		"자료/detail.HTML":    []byte(`<a href="../overview.html">back</a>`),
		"assets/app.js":     []byte(`document.body.dataset.running="ordinary";`),
		"arbitrary.unknown": {0, 255, 128, 1, 0},
		".hidden":           []byte("retained"),
		"manifest.json":     []byte("ordinary payload, not service metadata"),
	}
	for name, body := range files {
		Expect(os.WriteFile(filepath.Join(output, filepath.FromSlash(name)), body, 0600)).To(Succeed())
	}
	outside := filepath.Join(root, "outside")
	Expect(os.WriteFile(outside, []byte("unrelated source"), 0600)).To(Succeed())
	Expect(os.Symlink("../outside", filepath.Join(output, "link"))).To(Succeed())
	var archive bytes.Buffer
	checks := 0
	Expect(ArchiveDirectory(context.Background(), output, &archive, func() error { checks++; return nil })).To(Succeed())
	actual := map[string][]byte{}
	kinds := map[string]string{}
	links := map[string]string{}
	Expect(New().Expand(context.Background(), bytes.NewReader(archive.Bytes()), int64(archive.Len()), func(entry importer.EntryInput, body io.Reader) error {
		kinds[entry.Path] = entry.Kind
		links[entry.Path] = entry.LinkTarget
		if body != nil {
			b, err := io.ReadAll(body)
			actual[entry.Path] = b
			return err
		}
		return nil
	})).To(Succeed())
	Expect(actual).To(Equal(files))
	Expect(kinds).To(HaveLen(len(files) + 4)) // Three represented directories and a link.
	Expect(kinds["assets/empty"]).To(Equal("directory"))
	Expect(kinds["link"]).To(Equal("symlink"))
	Expect(links["link"]).To(Equal("../outside"))
	Expect(checks).To(BeNumerically(">", len(files)))
	b, err := os.ReadFile(outside)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(b)).To(Equal("unrelated source"))
})

var _ = It("GDIR02 does not finalize an output archive after current authority or cancellation is lost", Label("unit"), func() {
	for _, cause := range []error{domain.ErrDenied, context.Canceled, context.DeadlineExceeded} {
		root := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(root, "page.html"), bytes.Repeat([]byte("x"), 131072), 0600)).To(Succeed())
		var archive bytes.Buffer
		checks := 0
		err := ArchiveDirectory(context.Background(), root, &archive, func() error {
			checks++
			if checks >= 5 {
				return cause
			}
			return nil
		})
		Expect(errors.Is(err, cause)).To(BeTrue())
		_, err = zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
		Expect(err).To(HaveOccurred(), "partial output must not become a complete saved tree")
	}
})

var _ = It("GDIR03 detects a changed file and rejects a substituted symlink root without following it", Label("unit"), func() {
	root := GinkgoT().TempDir()
	name := filepath.Join(root, "page.html")
	Expect(os.WriteFile(name, bytes.Repeat([]byte("x"), 131072), 0600)).To(Succeed())
	var archive bytes.Buffer
	checks := 0
	err := ArchiveDirectory(context.Background(), root, &archive, func() error {
		checks++
		if checks == 5 {
			f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, writeErr := f.Write([]byte("changed"))
			return errors.Join(writeErr, f.Close())
		}
		return nil
	})
	Expect(errors.Is(err, importer.ErrInputChanged)).To(BeTrue())
	_, err = zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	Expect(err).To(HaveOccurred())
	alias := filepath.Join(GinkgoT().TempDir(), "alias")
	Expect(os.Symlink(root, alias)).To(Succeed())
	archive.Reset()
	Expect(ArchiveDirectory(context.Background(), alias, &archive, func() error { return nil })).To(MatchError(importer.ErrInputChanged))
	Expect(archive.Len()).To(BeZero())
})
