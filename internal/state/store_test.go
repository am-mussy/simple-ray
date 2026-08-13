package state

import (
	"os"
	"runtime"
	"testing"

	"github.com/mussy/simple-ray/internal/domain"
)

func TestStoreWritesSecretWithPrivatePermissions(t *testing.T) {
	store := New(t.TempDir())
	secret := domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: "canary-token"}
	if err := store.SaveSecrets(secret); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode is %o, want 600", info.Mode().Perm())
	}
	loaded, err := store.LoadSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIToken != secret.APIToken {
		t.Fatal("secret did not round-trip")
	}
}

func TestStoreRefusesSymlinkTarget(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation is not generally available to unprivileged Windows tests")
	}
	dir := t.TempDir()
	target := t.TempDir() + "/target"
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	store := New(dir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.SecretsPath()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecrets(domain.Secrets{SchemaVersion: 1, APIToken: "secret"}); err == nil {
		t.Fatal("expected symlink target to be refused")
	}
}

func TestEnsureSecuresExistingBackupsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission semantics")
	}
	dir := t.TempDir()
	backups := dir + "/backups"
	if err := os.Mkdir(backups, 0755); err != nil {
		t.Fatal(err)
	}
	store := New(dir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(backups)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("backups directory mode = %o, want 700", info.Mode().Perm())
	}
}
