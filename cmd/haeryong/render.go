package main

import (
	httprenderer "haeryong.local/haeryong/internal/adapters/http/renderer"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
	app "haeryong.local/haeryong/internal/application/storage"
)

// newRenderFacade wires the lazy renderer subsystem: the service (and the
// per-user origin ledger under it) is constructed only when the first
// render session opens, so ordinary daemon operation and untagged headless
// tests never initialize real per-user origin state.
func newRenderFacade(store app.Store, core *appauth.Service, bytes query.Bytes, managementOrigin func() string) *render.Lazy {
	return render.NewLazy(func() (*render.Service, error) {
		base, err := renderStateBase()
		if err != nil {
			return nil, &render.Error{Reason: render.ReasonUnsupportedAccount}
		}
		var service *render.Service
		options := render.Options{
			AppStateDir:      base,
			ManagementOrigin: managementOrigin,
			NewServer: func(origin string, binding render.Binding) render.Server {
				server := httprenderer.New(service, origin, binding)
				// The trusted management browser origin composes the public
				// Reader route for the top-level redirect document.
				server.SetPublicReaderBase(managementOrigin)
				return server
			},
		}
		service, err = render.New(store, core, bytes, options)
		return service, err
	})
}
