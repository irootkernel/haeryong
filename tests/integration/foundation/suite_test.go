package foundation_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"testing"
)

func TestSuite(t *testing.T) {
	if os.Getenv("HAERYONG_FOUNDATION_TEST_CHILD") == "1" {
		runFoundationChild(t)
		return
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "foundation")
}
