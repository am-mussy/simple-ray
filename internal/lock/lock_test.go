package lock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRecoversOldLockDirectoryWithoutMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vpnctl.lock")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	guard, err := Acquire(path, "test")
	if err != nil {
		t.Fatalf("old incomplete lock permanently blocks recovery: %v", err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
}
