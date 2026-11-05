package render

import (
	"context"
	"sync"
)

// Lazy defers renderer construction to the first render use: ordinary
// daemon operation and untagged headless tests never initialize per-user
// origin state.
type Lazy struct {
	mu      sync.Mutex
	new     func() (*Service, error)
	service *Service
	err     error
	closed  bool
}

// NewLazy wraps a constructor; a nil constructor reports an honest
// renderer-unavailable error on first use.
func NewLazy(new func() (*Service, error)) *Lazy {
	return &Lazy{new: new}
}

// Get returns the shared service, constructing it at most once.
func (l *Lazy) Get() (*Service, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, &Error{Reason: ReasonClosedSession}
	}
	if l.service != nil {
		return l.service, nil
	}
	if l.err != nil {
		return nil, l.err
	}
	if l.new == nil {
		l.err = &Error{Reason: ReasonUnsupportedAccount}
		return nil, l.err
	}
	service, err := l.new()
	if err != nil {
		l.err = err
		return nil, err
	}
	l.service = service
	return service, nil
}

// Shutdown does not initialize an unused renderer or its origin state.
func (l *Lazy) Shutdown() error {
	l.mu.Lock()
	l.closed = true
	service := l.service
	l.mu.Unlock()
	if service == nil {
		return nil
	}
	return service.Shutdown()
}

// Unavailable reports missing configuration, shutdown or any known constructor
// failure without invoking the factory or creating app state.
func (l *Lazy) Unavailable() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed || l.new == nil || l.err != nil
}

// Peek returns the initialized service without invoking the constructor or
// creating app state.
func (l *Lazy) Peek() *Service {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	return l.service
}

// Context carries the lazy facade through adapters without forcing use.
func (l *Lazy) Context() context.Context { return context.Background() }
