package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type storageFunc func(context.Context, Profile) (StorageState, func() error, error)

func (f storageFunc) Prepare(c context.Context, p Profile) (StorageState, func() error, error) {
	return f(c, p)
}

type endpointStub struct {
	started bool
	closed  bool
	onStart func()
	stop    func()
	done    chan error
}

func (e *endpointStub) Start(_ context.Context, _ Profile, id Identity, _ int, stop func()) (Identity, error) {
	e.started = true
	e.stop = stop
	e.done = make(chan error)
	if e.onStart != nil {
		e.onStart()
	}
	id.Endpoint = "http://127.0.0.1:9270"
	id.ControlReady = true
	return id, nil
}
func (e *endpointStub) Close(context.Context) error { e.closed = true; return nil }
func (e *endpointStub) Done() <-chan error          { return e.done }

func testSelection(t *testing.T) Selection {
	t.Helper()
	// Prefer the mounted writable scratch volume even for direct developer runs.
	parent := os.TempDir()
	if requireMount("/Volumes/RootKernel") == nil {
		candidate := "/Volumes/RootKernel/tmp"
		if path, err := os.MkdirTemp(candidate, "haeryong-service-tests-*"); err == nil {
			parent = path
			t.Cleanup(func() { os.RemoveAll(path) })
		}
	}
	path, err := os.MkdirTemp(parent, "profile-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(path) })
	s := Selection{Profile: "test", DataDir: filepath.Join(path, "library")}
	if _, err := Initialize(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStorageReadinessAndShutdownOwnership(t *testing.T) {
	s := testSelection(t)
	endpoint := &endpointStub{}
	closed := false
	storage := storageFunc(func(_ context.Context, p Profile) (StorageState, func() error, error) {
		if writer, err := OpenWriter(s); !errors.Is(err, ErrLocked) {
			if writer != nil {
				writer.Close()
			}
			t.Fatal("storage prepared without writer lock")
		}
		if _, err := os.Stat(filepath.Join(p.DataDir, "runtime", "endpoint.json")); !os.IsNotExist(err) {
			t.Fatal("readiness before storage preparation")
		}
		return StorageState{Ready: true, State: "test_prepared"}, func() error {
			closed = true
			if !endpoint.closed {
				t.Error("storage closed before endpoint drained")
			}
			if writer, err := OpenWriter(s); !errors.Is(err, ErrLocked) {
				if writer != nil {
					writer.Close()
				}
				t.Error("writer released before storage close")
			}
			return nil
		}, nil
	})
	runner := Runner{Selection: s, Port: DefaultPort, Storage: storage, Endpoint: endpoint}
	err := runner.Run(context.Background(), func(id Identity) {
		if !id.Storage.Ready {
			t.Error("storage readiness discarded")
		}
		p, _ := Load(s)
		discovered, err := ReadDiscovery(p)
		if err != nil || discovered != id {
			t.Errorf("published identity mismatch: %v", err)
		}
		endpoint.stop()
	})
	if err != nil || !closed {
		t.Fatalf("shutdown: %v closed=%v", err, closed)
	}
	writer, err := OpenWriter(s)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
}

func TestStorageFailureDoesNotAdvertiseOrRetainLock(t *testing.T) {
	s := testSelection(t)
	endpoint := &endpointStub{}
	failure := errors.New("migration/recovery not ready")
	runner := Runner{Selection: s, Port: DefaultPort, Storage: storageFunc(func(context.Context, Profile) (StorageState, func() error, error) {
		return StorageState{}, nil, failure
	}), Endpoint: endpoint}
	err := runner.Run(context.Background(), func(Identity) { t.Error("advertised failed storage") })
	if !errors.Is(err, failure) || endpoint.started {
		t.Fatalf("storage failure not fenced: %v", err)
	}
	writer, err := OpenWriter(s)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
}

func TestProfilePrivateRecordsAndIdentityBinding(t *testing.T) {
	s := testSelection(t)
	p, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Profile = "other"
	if _, err := Load(s); err == nil {
		t.Fatal("accepted wrong profile name")
	}
	s.Profile = p.Name
	path := filepath.Join(p.DataDir, "profile.json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(s); err == nil {
		t.Fatal("accepted exposed credential file")
	}
	os.Chmod(path, 0600)
	original := path + ".original"
	if err := os.Rename(path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(s); err == nil {
		t.Fatal("followed credential symlink")
	}
}

func TestRunnerPortBoundaries(t *testing.T) {
	s := testSelection(t)
	for _, port := range []int{1, 1023, 1024, 9000, 9999, 65535, 65536} {
		endpoint := &endpointStub{}
		runner := Runner{Selection: s, Port: port, Storage: pendingFixture{}, Endpoint: endpoint}
		err := runner.Run(context.Background(), func(Identity) { endpoint.stop() })
		if port < 1024 || port > 65535 {
			if err == nil || !strings.Contains(err.Error(), "1024 and 65535") || endpoint.started {
				t.Fatalf("invalid port %d reached endpoint: %v", port, err)
			}
		} else if err != nil || !endpoint.started {
			t.Fatalf("valid port %d rejected: %v", port, err)
		}
	}
}

type pendingFixture struct{}

func (pendingFixture) Prepare(context.Context, Profile) (StorageState, func() error, error) {
	return StorageState{State: "fixture_pending"}, func() error { return nil }, nil
}
