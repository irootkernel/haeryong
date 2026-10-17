package main

import (
	"net/http"
)

// productRoutes composes only adapters that explicitly own the current route.
// Authentication/Host/Origin remains on the single surrounding management router.
type productRoutes []http.Handler

func (routes productRoutes) Handles(r *http.Request) bool {
	for _, h := range routes {
		if route, ok := h.(interface{ Handles(*http.Request) bool }); ok && route.Handles(r) {
			return true
		}
	}
	return false
}
func (routes productRoutes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, h := range routes {
		if route, ok := h.(interface{ Handles(*http.Request) bool }); ok && route.Handles(r) {
			h.ServeHTTP(w, r)
			return
		}
	}
	http.NotFound(w, r)
}
