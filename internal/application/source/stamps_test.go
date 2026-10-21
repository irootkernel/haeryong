package source_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// S13: fixed-width stamps order correctly within a second.
var _ = It("TestFixedWidthCheckStampsOrderWithinASecond", Label("unit"), func() {
	stamp := func(millis int) string {
		return time.Unix(1000000, 0).UTC().Add(time.Duration(millis) * time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}
	a, b := stamp(120), stamp(123)
	Expect(len(a)).To(Equal(len(b)))
	Expect(a < b).To(BeTrue(), "text ordering must hold within a second")
	Expect(strings.HasSuffix(a, "120Z")).To(BeTrue())
})
