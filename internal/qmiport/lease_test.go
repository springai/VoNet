package qmiport

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaseKeepsPortOpenAndSerializesUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wwan0qmi0")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	coordinator := newCoordinator(func(path string) (portHandle, error) {
		opens.Add(1)
		return os.OpenFile(path, os.O_RDWR, 0)
	})
	t.Cleanup(func() { _ = coordinator.close() })

	first, err := coordinator.acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := coordinator.acquire(ctx, path); err == nil {
		t.Fatal("second acquire succeeded before the first lease was released")
	}
	first.Release()

	second, err := coordinator.acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	second.Release()
	if got := opens.Load(); got != 1 {
		t.Fatalf("keepalive opens = %d, want 1", got)
	}
}

func TestLeaseReopensReplacedDeviceNode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "wwan0qmi0")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	identityOne := filepath.Join(directory, "identity-one")
	identityTwo := filepath.Join(directory, "identity-two")
	for _, identityPath := range []string{identityOne, identityTwo} {
		if err := os.WriteFile(identityPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	identityOneInfo, err := os.Stat(identityOne)
	if err != nil {
		t.Fatal(err)
	}
	identityTwoInfo, err := os.Stat(identityTwo)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := newCoordinator(func(_ string) (portHandle, error) {
		if opens.Add(1) == 1 {
			return &testPortHandle{info: identityOneInfo}, nil
		}
		return &testPortHandle{info: identityTwoInfo}, nil
	})
	t.Cleanup(func() { _ = coordinator.close() })

	first, err := coordinator.acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
	if got := opens.Load(); got != 2 {
		t.Fatalf("keepalive opens = %d, want 2 after node replacement", got)
	}
}

type testPortHandle struct {
	info os.FileInfo
}

func (*testPortHandle) Close() error { return nil }

func (handle *testPortHandle) Stat() (os.FileInfo, error) {
	return handle.info, nil
}
