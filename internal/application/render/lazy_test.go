package render_test

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/render"
)

var _ = It("closes an unused renderer without initializing account state or invoking its factory", Label("unit"), func() {
	called := false
	lazy := render.NewLazy(func() (*render.Service, error) { called = true; return nil, nil })
	Expect(lazy.Shutdown()).To(Succeed())
	Expect(lazy.Unavailable()).To(BeTrue())
	_, err := lazy.Get()
	Expect(err).To(MatchError(&render.Error{Reason: render.ReasonClosedSession}))
	Expect(called).To(BeFalse())
})

var _ = It("reports missing or failed renderer construction without initiating account state", Label("unit"), func() {
	Expect(render.NewLazy(nil).Unavailable()).To(BeTrue())
	for _, failure := range []error{errors.New("account lookup failure"), &render.Error{Reason: render.ReasonUnsupportedAccount}} {
		calls := 0
		lazy := render.NewLazy(func() (*render.Service, error) { calls++; return nil, failure })
		Expect(lazy.Unavailable()).To(BeFalse())
		Expect(calls).To(BeZero())
		_, err := lazy.Get()
		Expect(err).To(MatchError(failure))
		Expect(lazy.Unavailable()).To(BeTrue())
		_, err = lazy.Get()
		Expect(err).To(MatchError(failure))
		Expect(calls).To(Equal(1))
	}
})
