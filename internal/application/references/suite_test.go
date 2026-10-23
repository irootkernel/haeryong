package references

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestReferences(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Pinned reference work") }
