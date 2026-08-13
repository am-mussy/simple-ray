package lock

import (
	"path/filepath"
	"testing"
)

func TestAcquireSerializesAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "lock")
	first, err := Acquire(path, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, "second"); err == nil {
		t.Fatal("concurrent mutation acquired the lock")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path, "second")
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseCannotRunTwice(t *testing.T) {
	guard, err := Acquire(filepath.Join(t.TempDir(), "runtime", "lock"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err == nil {
		t.Fatal("second release succeeded")
	}
}
