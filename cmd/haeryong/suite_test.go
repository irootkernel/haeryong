package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSuite(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "haeryong") }

var _ = BeforeSuite(func() {
	root := GinkgoT().TempDir()
	testBinary = filepath.Join(root, "haeryong")
	command := exec.Command("go", "build", "-race", "-o", testBinary, ".")
	output, err := command.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
})
