package source_test

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	adapter "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/source"
	"io"
	"os"
	"path/filepath"
)

var _ = It("SCAP01 actual opened files keep arbitrary binary bytes and reject outside symlinks and known live paths", Label("unit"), func() {
	dir := GinkgoT().TempDir()
	root := filepath.Join(dir, "selected")
	Expect(os.Mkdir(root, 0700)).To(Succeed())
	body := []byte{0, 255, 13, 10, 1}
	Expect(os.WriteFile(filepath.Join(root, ".ordinary.unknown"), body, 0600)).To(Succeed())
	outside := filepath.Join(dir, "outside")
	Expect(os.WriteFile(outside, body, 0600)).To(Succeed())
	Expect(os.Symlink(outside, filepath.Join(root, "escape"))).To(Succeed())
	reader := adapter.Files{ExcludedPaths: []string{filepath.Join(root, "auth-store")}}
	Expect(os.WriteFile(filepath.Join(root, "auth-store"), body, 0600)).To(Succeed())
	stream, e := reader.Open(context.Background(), root, ".ordinary.unknown")
	Expect(e).NotTo(HaveOccurred())
	got, e := io.ReadAll(stream)
	Expect(e).NotTo(HaveOccurred())
	Expect(stream.Close()).To(Succeed())
	Expect(got).To(Equal(body))
	_, e = reader.Open(context.Background(), root, "escape")
	Expect(e).To(MatchError(app.ErrInvalidLocation))
	_, e = reader.Open(context.Background(), root, "auth-store")
	Expect(e).To(MatchError(app.ErrSourceDenied))
	Expect(os.Link(filepath.Join(root, "auth-store"), filepath.Join(root, "hard-alias"))).To(Succeed())
	_, e = reader.Open(context.Background(), root, "hard-alias")
	Expect(e).To(MatchError(app.ErrSourceDenied))
	Expect(os.Symlink(filepath.Join(dir, "missing-outside"), filepath.Join(root, "missing-escape"))).To(Succeed())
	_, e = reader.Open(context.Background(), root, "missing-escape")
	Expect(e).To(MatchError(app.ErrInvalidLocation), "out-of-root existence must not change refusal")
	Expect(os.WriteFile(filepath.Join(root, "auth.json"), body, 0600)).To(Succeed())
	ordinary, e := reader.Open(context.Background(), root, "auth.json")
	Expect(e).NotTo(HaveOccurred())
	Expect(ordinary.Close()).To(Succeed(), "ordinary names are not secret heuristics")
})

var _ = It("SCAP25 protected directory identities fence Git preflight aliases and actual opened case aliases", Label("unit"), func() {
	dir := GinkgoT().TempDir()
	profile := filepath.Join(dir, "OwnedProfile")
	Expect(os.Mkdir(profile, 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(profile, "profile.json"), []byte("synthetic-bootstrap-control"), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "ordinary"), []byte("ordinary source"), 0600)).To(Succeed())
	Expect(os.Symlink(profile, filepath.Join(dir, "profile-alias"))).To(Succeed())
	reader := adapter.Files{ExcludedPaths: []string{profile}}
	ordinary, e := reader.Open(context.Background(), dir, "ordinary")
	Expect(e).NotTo(HaveOccurred())
	Expect(ordinary.Close()).To(Succeed())
	Expect(reader.CheckLocation(context.Background(), dir, "profile-alias/profile.json")).To(MatchError(app.ErrSourceDenied))
	// A real case alias is exercised when this filesystem actually supports it.
	if alias, e := os.Stat(filepath.Join(dir, "ownedprofile")); e == nil {
		AddReportEntry("case alias applicability", "actual same-directory identity case variant exercised through Open and missing-leaf Git preflight")
		original, e := os.Stat(profile)
		Expect(e).NotTo(HaveOccurred())
		Expect(os.SameFile(alias, original)).To(BeTrue())
		stream, e := reader.Open(context.Background(), dir, "ownedprofile/profile.json")
		if stream != nil {
			Expect(stream.Close()).To(Succeed())
		}
		Expect(e).To(MatchError(app.ErrSourceDenied))
		Expect(reader.CheckLocation(context.Background(), dir, "ownedprofile/not-present-yet")).To(MatchError(app.ErrSourceDenied))
	} else {
		AddReportEntry("case alias applicability", "filesystem does not expose this case alias; symlink/directory identity checks still executed")
	}
})

var _ = It("SCAP26 actual managed factory excludes the native default client bearer store without filename heuristics", Label("unit"), func() {
	dir := GinkgoT().TempDir()
	GinkgoT().Setenv("HOME", dir)
	GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	GinkgoT().Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	GinkgoT().Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	config, e := os.UserConfigDir()
	Expect(e).NotTo(HaveOccurred())
	clients := filepath.Join(config, "haeryong", "clients", "fixture")
	Expect(os.MkdirAll(clients, 0700)).To(Succeed())
	secret := filepath.Join(clients, "credential.json")
	Expect(os.WriteFile(secret, []byte("synthetic-native-owner-bearer"), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "credential.json"), []byte("ordinary source name"), 0600)).To(Succeed())
	profile := filepath.Join(dir, "profile")
	Expect(os.Mkdir(profile, 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(profile, "profile.json"), []byte("synthetic-bootstrap-control"), 0600)).To(Succeed())
	Expect(os.Link(filepath.Join(profile, "profile.json"), filepath.Join(dir, "bootstrap-alias"))).To(Succeed())
	Expect(os.Link(secret, filepath.Join(dir, "client-bearer-alias"))).To(Succeed())
	profilesRoot, e := service.DefaultProfilesRoot()
	Expect(e).NotTo(HaveOccurred())
	profileRelative, e := filepath.Rel(dir, profilesRoot)
	Expect(e).NotTo(HaveOccurred())
	Expect(filepath.IsLocal(profileRelative)).To(BeTrue(), "default profile root must remain inside this fixture before any profile write")
	second := filepath.Join(profilesRoot, "second", "profile.json")
	Expect(os.MkdirAll(filepath.Dir(second), 0700)).To(Succeed())
	Expect(os.WriteFile(second, []byte("synthetic-second-profile-control"), 0600)).To(Succeed())
	Expect(os.Symlink(profilesRoot, filepath.Join(dir, "profiles-alias"))).To(Succeed())
	reader := adapter.ManagedFiles(profile, profilesRoot, secret)
	for _, path := range []string{second, filepath.Join(dir, "profiles-alias", "second", "profile.json")} {
		relative, e := filepath.Rel(dir, path)
		Expect(e).NotTo(HaveOccurred())
		Expect(reader.CheckLocation(context.Background(), dir, relative)).To(MatchError(app.ErrSourceDenied))
		stream, e := reader.Open(context.Background(), dir, relative)
		Expect(stream).To(BeNil())
		Expect(e).To(MatchError(app.ErrSourceDenied))
	}
	ordinaryProfiles := filepath.Join(dir, "ordinary-profiles", "second", "profile.json")
	Expect(os.MkdirAll(filepath.Dir(ordinaryProfiles), 0700)).To(Succeed())
	Expect(os.WriteFile(ordinaryProfiles, []byte("ordinary source name"), 0600)).To(Succeed())
	relativeOrdinary, e := filepath.Rel(dir, ordinaryProfiles)
	Expect(e).NotTo(HaveOccurred())
	ordinaryControl, e := reader.Open(context.Background(), dir, relativeOrdinary)
	Expect(e).NotTo(HaveOccurred())
	Expect(ordinaryControl.Close()).To(Succeed())
	ordinary, e := reader.Open(context.Background(), dir, "credential.json")
	Expect(e).NotTo(HaveOccurred())
	Expect(ordinary.Close()).To(Succeed())
	relative, e := filepath.Rel(dir, secret)
	Expect(e).NotTo(HaveOccurred())
	stream, e := reader.Open(context.Background(), dir, relative)
	if stream != nil {
		Expect(stream.Close()).To(Succeed())
	}
	Expect(e).To(MatchError(app.ErrSourceDenied))
	Expect(reader.CheckLocation(context.Background(), dir, relative)).To(MatchError(app.ErrSourceDenied))
	for _, alias := range []string{"bootstrap-alias", "client-bearer-alias"} {
		stream, e := reader.Open(context.Background(), dir, alias)
		if stream != nil {
			Expect(stream.Close()).To(Succeed())
		}
		Expect(e).To(MatchError(app.ErrSourceDenied))
		Expect(reader.CheckLocation(context.Background(), dir, alias)).To(MatchError(app.ErrSourceDenied))
	}
})
