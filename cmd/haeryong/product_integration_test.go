package main

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/application/service"

	"haeryong.local/haeryong/internal/domain"

	"os"
	"path/filepath"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func callerProfile(t GinkgoTInterface, s service.Selection, session domain.Session, token, base string) (string, []string) {
	GinkgoHelper()
	root := filepath.Join(t.TempDir(), "caller")
	{
		e := os.Mkdir(root, 0700)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	path := filepath.Join(root, "credential.json")
	b, _ := json.Marshal(client.Credential{Endpoint: base, Profile: s.Profile, LibraryID: session.LibraryID, Token: token})
	{
		e := os.WriteFile(path, b, 0600)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	return root, []string{"--profile", s.Profile, "--client-dir", root, "--credential", path, "--json"}
}
