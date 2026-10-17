package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Storage is the readiness seam for TASK-002-02 migrations and recovery. Prepare
// runs under the writer lock, before endpoint publication. Its closer runs after
// listener shutdown and before writer release, including failed startup.
type Storage interface {
	Prepare(context.Context, Profile) (StorageState, func() error, error)
}

type StorageState struct {
	Ready bool   `json:"ready"`
	State string `json:"state"`
}

type Identity struct {
	LibraryID    string       `json:"library_id"`
	ProfileID    string       `json:"profile_id"`
	InstanceID   string       `json:"instance_id"`
	API          string       `json:"api"`
	Endpoint     string       `json:"endpoint"`
	ControlReady bool         `json:"control_ready"`
	Storage      StorageState `json:"storage"`
}

// Endpoint owns the configured listener. Start must bind exactly the requested
// port and serve only local control when Storage.Ready is false.
type Endpoint interface {
	Start(context.Context, Profile, Identity, int, func()) (Identity, error)
	Close(context.Context) error
	Done() <-chan error
}

type Runner struct {
	Selection    Selection
	Port         int
	Storage      Storage
	Endpoint     Endpoint
	ProfileFault func(string) error
	Work         func(context.Context) error
	ListenerPort func(context.Context) (int, error)
}

func (r Runner) Run(ctx context.Context, onReady func(Identity)) (result error) {
	if r.Port < 1024 || r.Port > 65535 {
		return errors.New("listener port must be between 1024 and 65535")
	}
	writer, err := OpenWriter(r.Selection)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, writer.Close()) }()
	if r.Storage == nil || r.Endpoint == nil {
		return errors.New("storage and endpoint required")
	}
	if err := writer.PrepareStorageBootstrap(r.ProfileFault); err != nil {
		return err
	}
	storage, closeStorage, err := r.Storage.Prepare(ctx, writer.Profile)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, closeStorage()) }()
	if storage.Ready {
		if err := writer.MarkStorageReady(r.ProfileFault); err != nil {
			return err
		}
	}
	if r.ListenerPort != nil {
		r.Port, err = r.ListenerPort(ctx)
		if err != nil {
			return err
		}
		if r.Port < 1024 || r.Port > 65535 {
			return errors.New("configured listener port must be between 1024 and 65535")
		}
	}
	instance, err := NewID()
	if err != nil {
		return err
	}
	controlCtx, stop := context.WithCancel(ctx)
	defer stop()
	id := Identity{LibraryID: writer.Profile.LibraryID, ProfileID: writer.Profile.ProfileID, InstanceID: instance, API: APIVersion, Storage: storage}
	id, err = r.Endpoint.Start(controlCtx, writer.Profile, id, r.Port, stop)
	if err != nil {
		return err
	}
	var workDone <-chan error
	if r.Work != nil {
		workCtx, stopWork := context.WithCancel(controlCtx)
		done := make(chan error, 1)
		workDone = done
		go func() { done <- r.Work(workCtx); close(done) }()
		defer func() { stopWork(); result = errors.Join(result, <-done) }()
	}
	// The adapter's shutdown is bounded; it owns any graceful-drain timeout.
	defer func() { result = errors.Join(result, r.Endpoint.Close(context.Background())) }()
	if err := writeJSON(filepath.Join(writer.Profile.DataDir, "runtime"), "endpoint.json", id); err != nil {
		return err
	}
	defer func() {
		err := os.Remove(filepath.Join(writer.Profile.DataDir, "runtime", "endpoint.json"))
		if err == nil {
			err = syncDir(filepath.Join(writer.Profile.DataDir, "runtime"))
		}
		result = errors.Join(result, err)
	}()
	onReady(id)
	select {
	case <-controlCtx.Done():
		return nil
	case err := <-workDone:
		workDone = nil
		return err
	case err := <-r.Endpoint.Done():
		return err
	}
}

func ReadDiscovery(p Profile) (Identity, error) {
	var id Identity
	err := readJSON(filepath.Join(p.DataDir, "runtime", "endpoint.json"), &id)
	if err != nil {
		return id, err
	}
	if id.LibraryID != p.LibraryID || id.ProfileID != p.ProfileID || id.API != APIVersion || !validID(id.InstanceID) || !id.ControlReady {
		return Identity{}, errors.New("discovery library/profile/API identity mismatch")
	}
	return id, nil
}
