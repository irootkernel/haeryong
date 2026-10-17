package service

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
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

func testSelection(t GinkgoTInterface) Selection {
	GinkgoHelper()
	// Prefer the mounted writable scratch volume even for direct developer runs.
	parent := os.TempDir()
	if requireMount("/Volumes/RootKernel") == nil {
		candidate := "/Volumes/RootKernel/tmp"
		if path, err := os.MkdirTemp(candidate, "haeryong-service-tests-*"); err == nil {
			parent = path
			DeferCleanup(func() { os.RemoveAll(path) })
		}
	}
	path, err := os.MkdirTemp(parent, "profile-*")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { os.RemoveAll(path) })
	s := Selection{Profile: "test", DataDir: filepath.Join(path, "library")}
	{
		_, err := Initialize(s)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return s
}

var _ = It("TestStorageReadinessAndShutdownOwnership", Label("integration"), func() {
	t := GinkgoT()

	s := testSelection(t)
	endpoint := &endpointStub{}
	closed := false
	storage := storageFunc(func(_ context.Context, p Profile) (StorageState, func() error, error) {
		if writer, err := OpenWriter(s); !errors.Is(err, ErrLocked) {
			if writer != nil {
				writer.Close()
			}
			Fail(fmt.Sprint("storage prepared without writer lock"))
		}
		{
			_, err := os.Stat(filepath.Join(p.DataDir, "runtime", "endpoint.json"))
			Expect(!os.IsNotExist(err)).To(BeFalse(), func() string { return fmt.Sprint("readiness before storage preparation") })
		}

		return StorageState{Ready: true, State: "test_prepared"}, func() error {
			closed = true
			Expect(!endpoint.closed).To(BeFalse(), func() string { return fmt.Sprint("storage closed before endpoint drained") })

			if writer, err := OpenWriter(s); !errors.Is(err, ErrLocked) {
				if writer != nil {
					writer.Close()
				}
				Fail(fmt.Sprint("writer released before storage close"))
			}
			return nil
		}, nil
	})
	runner := Runner{Selection: s, Port: DefaultPort, Storage: storage, Endpoint: endpoint}
	err := runner.Run(context.Background(), func(id Identity) {
		Expect(!id.Storage.Ready).To(BeFalse(), func() string { return fmt.Sprint("storage readiness discarded") })

		p, _ := Load(s)
		discovered, err := ReadDiscovery(p)
		Expect(err != nil || discovered != id).To(BeFalse(), func() string { return fmt.Sprintf("published identity mismatch: %v", err) })

		endpoint.stop()
	})
	Expect(err != nil || !closed).To(BeFalse(), func() string { return fmt.Sprintf("shutdown: %v closed=%v", err, closed) })

	writer, err := OpenWriter(s)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	writer.Close()
})
var _ = It("TestStorageFailureDoesNotAdvertiseOrRetainLock", Label("integration"), func() {
	t := GinkgoT()

	s := testSelection(t)
	endpoint := &endpointStub{}
	failure := errors.New("migration/recovery not ready")
	runner := Runner{Selection: s, Port: DefaultPort, Storage: storageFunc(func(context.Context, Profile) (StorageState, func() error, error) {
		return StorageState{}, nil, failure
	}), Endpoint: endpoint}
	err := runner.Run(context.Background(), func(Identity) { Fail(fmt.Sprint("advertised failed storage")) })
	Expect(!errors.Is(err, failure) || endpoint.started).To(BeFalse(), func() string { return fmt.Sprintf("storage failure not fenced: %v", err) })

	writer, err := OpenWriter(s)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	writer.Close()
})
var _ = It("TestProfilePrivateRecordsAndIdentityBinding", Label("integration"), func() {
	t := GinkgoT()

	s := testSelection(t)
	p, err := Load(s)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	s.Profile = "other"
	{
		_, err := Load(s)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("accepted wrong profile name") })
	}

	s.Profile = p.Name
	path := filepath.Join(p.DataDir, "profile.json")
	{
		err := os.Chmod(path, 0644)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := Load(s)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("accepted exposed credential file") })
	}

	os.Chmod(path, 0600)
	original := path + ".original"
	{
		err := os.Rename(path, original)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := os.Symlink(original, path)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := Load(s)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("followed credential symlink") })
	}

})
var _ = It("TestRunnerPortBoundaries", Label("integration"), func() {
	t := GinkgoT()

	s := testSelection(t)
	for _, port := range []int{1, 1023, 1024, 9000, 9999, 65535, 65536} {
		endpoint := &endpointStub{}
		runner := Runner{Selection: s, Port: port, Storage: pendingFixture{}, Endpoint: endpoint}
		err := runner.Run(context.Background(), func(Identity) { endpoint.stop() })
		if port < 1024 || port > 65535 {
			Expect(err == nil || !strings.Contains(err.Error(), "1024 and 65535") || endpoint.started).To(BeFalse(), func() string { return fmt.Sprintf("invalid port %d reached endpoint: %v", port, err) })

		} else if err != nil || !endpoint.started {
			Fail(fmt.Sprintf("valid port %d rejected: %v", port, err))
		}
	}
})

type pendingFixture struct{}

func (pendingFixture) Prepare(context.Context, Profile) (StorageState, func() error, error) {
	return StorageState{State: "fixture_pending"}, func() error { return nil }, nil
}
