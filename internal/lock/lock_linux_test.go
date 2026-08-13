//go:build linux

package lock

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAcquireRejectsUnsafeRuntimeDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(filepath.Join(parent, "lock"), "test"); err == nil {
		t.Fatal("unsafe runtime directory was accepted")
	}
}

func TestAcquireRejectsSymlinkRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "runtime")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(filepath.Join(link, "lock"), "test"); err == nil {
		t.Fatal("symlink runtime directory was accepted")
	}
}

func TestAcquireRejectsUnsafeLockFile(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "lock")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, "test"); err == nil {
		t.Fatal("unsafe lock file was accepted")
	}
}

func TestAcquireRejectsHardLinkedLockFile(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "runtime")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(parent, "lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(filepath.Join(parent, "lock"), "test"); err == nil {
		t.Fatal("hard-linked lock file was accepted")
	}
}

func TestFlockIsReleasedWhenProcessDies(t *testing.T) {
	if os.Getenv("VPNCTL_LOCK_HELPER") == "1" {
		guard, err := Acquire(os.Getenv("VPNCTL_LOCK_PATH"), "helper")
		if err != nil {
			os.Exit(2)
		}
		_ = guard
		fmt.Println("locked")
		select {}
	}

	path := filepath.Join(t.TempDir(), "runtime", "lock")
	command := exec.Command(os.Args[0], "-test.run=TestFlockIsReleasedWhenProcessDies")
	command.Env = append(os.Environ(), "VPNCTL_LOCK_HELPER=1", "VPNCTL_LOCK_PATH="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "locked\n" {
		_ = command.Process.Kill()
		t.Fatalf("helper did not acquire lock: %q, %v", line, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	guard, err := Acquire(path, "parent")
	if err != nil {
		t.Fatalf("kernel did not release flock after process exit: %v", err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
}
