//go:build linux

package installer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mussy/simple-ray/internal/state"
	"github.com/mussy/simple-ray/internal/xui"
)

type stoppedRunner struct {
	calls []string
}

func (r *stoppedRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if strings.Contains(call, "is-active") {
		return nil, errors.New("inactive")
	}
	return nil, nil
}

func (r *stoppedRunner) RunInput(ctx context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	return r.Run(ctx, name, args...)
}

func TestInitializeJournalRollbackRestoresCleanBaseline(t *testing.T) {
	parent := t.TempDir()
	store := state.New(filepath.Join(parent, "vpnctl"))
	manager := &Manager{Store: store}
	tx := journal{InstallID: "0123456789abcdef", StoreCreated: true}
	if err := manager.initializeJournal(tx, false); err != nil {
		t.Fatal(err)
	}
	if err := manager.removeOwnedStore(tx.InstallID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(store.Dir); !os.IsNotExist(err) {
		t.Fatalf("state directory remains after rollback: %v", err)
	}
}

func TestReusableStoreSupportsUninstallReinstallLifecycle(t *testing.T) {
	parent := t.TempDir()
	store := state.New(filepath.Join(parent, "vpnctl"))
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{Store: store}
	tx := journal{InstallID: "0123456789abcdef"}
	if err := manager.initializeJournal(tx, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manager.journalPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Dir, ".vpnctl-install-owned")); err != nil {
		t.Fatal(err)
	}
	if err := verifyReusableStore(store); err != nil {
		t.Fatal(err)
	}
}

func TestRealityPayloadMatchesV350Shape(t *testing.T) {
	payload := realityPayload(Request{ListenPort: 443, RealityTarget: "example.com:443", RealitySNI: "example.com"}, xui.KeyPair{PrivateKey: "private", PublicKey: "public"}, "abcdef0123456789")
	stream := map[string]any{}
	if err := json.Unmarshal([]byte(payload["streamSettings"].(string)), &stream); err != nil {
		t.Fatal(err)
	}
	if stream["network"] != "tcp" || stream["security"] != "reality" {
		t.Fatalf("stream settings = %#v", payload["streamSettings"])
	}
	reality, ok := stream["realitySettings"].(map[string]any)
	if !ok || reality["target"] != "example.com:443" || reality["privateKey"] != "private" {
		t.Fatalf("reality settings = %#v", stream["realitySettings"])
	}
	// Xray-core 26.7.11 defaults minClientVer to 26.3.27 when this is absent or
	// empty, which rejects mainstream clients. Rejected clients are relayed to
	// the decoy site rather than disconnected, so the VPN looks connected and
	// carries no traffic. The value must be set explicitly and permissively.
	if reality["minClientVer"] != realityMinClientVer {
		t.Fatalf("minimum client version = %#v, want %q", reality["minClientVer"], realityMinClientVer)
	}
}

func TestParseTokenRejectsAmbiguousOutput(t *testing.T) {
	if _, err := parseToken("apiToken: abcdefghijklmnopqrst\napiToken: zyxwvutsrqponmlkjihg\n"); err == nil {
		t.Fatal("ambiguous token output was accepted")
	}
}

func TestPrivilegedPortBoundaryRejectsUnprivilegedLoopbackImpersonation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_unprivileged_port_start")
	if err := os.WriteFile(path, []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requirePrivilegedPortBoundary(path); err == nil {
		t.Fatal("unsafe unprivileged port boundary was accepted")
	}
	if err := os.WriteFile(path, []byte("1024\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requirePrivilegedPortBoundary(path); err != nil {
		t.Fatal(err)
	}
}

func TestSecureDataTreeRemovesGroupAndWorldPermissions(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "database.db")
	if err := os.WriteFile(file, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := secureDataTree(root); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]os.FileMode{root: 0700, nested: 0700, file: 0600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != expected {
			t.Fatalf("%s mode = %o", path, info.Mode().Perm())
		}
	}
}

func TestSecureProgramTreeRestoresExecutableTraversalAfterRestrictiveUmask(t *testing.T) {
	root := filepath.Join(t.TempDir(), "x-ui")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "x-ui")
	if err := os.WriteFile(executable, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, "asset.dat")
	if err := os.WriteFile(asset, []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".vpnctl-owned")
	if err := os.WriteFile(marker, []byte("id"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := secureProgramTree(root); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]os.FileMode{root: 0755, executable: 0755, asset: 0644, marker: 0600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != expected {
			t.Fatalf("%s mode = %o", path, info.Mode().Perm())
		}
	}
}

func TestStopManagedServiceConfirmsInactiveBeforeCleanup(t *testing.T) {
	runner := &stoppedRunner{}
	if err := stopManagedService(context.Background(), runner); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl disable --now x-ui.service", "systemctl is-active --quiet x-ui.service"}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
	for index := range want {
		if runner.calls[index] != want[index] {
			t.Fatalf("calls = %#v", runner.calls)
		}
	}
}
