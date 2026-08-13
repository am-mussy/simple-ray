//go:build linux

package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type runnerCall struct {
	Name string
	Args []string
}

type fakeRunner struct {
	responses map[string][]byte
	calls     []runnerCall
}

func (r *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.RunInput(ctx, nil, name, args...)
}

func (r *fakeRunner) RunInput(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, runnerCall{Name: name, Args: append([]string(nil), args...)})
	key := strings.Join(append([]string{name}, args...), " ")
	value, ok := r.responses[key]
	if !ok {
		return nil, errors.New("unexpected command: " + key)
	}
	return value, nil
}

func TestParseDefaultInputPolicy(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: `DEFAULT_INPUT_POLICY="DROP"`, want: "deny"},
		{input: `DEFAULT_INPUT_POLICY="REJECT"`, want: "reject"},
		{input: `DEFAULT_INPUT_POLICY="ACCEPT"`, want: "allow"},
	} {
		got, err := parseDefaultInputPolicy(test.input)
		if err != nil || got != test.want {
			t.Fatalf("parseDefaultInputPolicy(%q) = %q, %v", test.input, got, err)
		}
	}
}

func TestFirewallPreflightUsesInactiveConfiguredPolicy(t *testing.T) {
	defaults := filepath.Join(t.TempDir(), "ufw")
	if err := os.WriteFile(defaults, []byte("IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{responses: map[string][]byte{
		"ufw version":        []byte("ufw 0.36.2"),
		"nft list ruleset":   nil,
		"ufw status verbose": []byte("Status: inactive\n"),
	}}
	snapshot, err := (Firewall{Runner: runner, DefaultsPath: defaults}).Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Active || snapshot.DefaultIncoming != "deny" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestFirewallRemoveRefusesAdministratorDrift(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{"ufw status verbose": []byte("Status: active\nnew admin rule\n")}}
	err := (Firewall{Runner: runner}).RemoveRules(context.Background(), nil, FirewallSnapshot{ManagedSHA256: strings.Repeat("0", 64), DefaultIncoming: "deny"})
	if err == nil || !strings.Contains(err.Error(), "administrator changes") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("firewall was mutated after drift: %#v", runner.calls)
	}
}

func TestVerifySSHListenerRejectsWrongExplicitPort(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "192.0.2.10 54321 198.51.100.20 22")
	runner := &fakeRunner{responses: map[string][]byte{"ss -H -ltnp": []byte("LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:((\"sshd\",pid=1,fd=3))\n")}}
	if err := verifySSHListener(context.Background(), runner, 2222); err == nil {
		t.Fatal("wrong explicit SSH port was accepted")
	}
	if err := verifySSHListener(context.Background(), runner, 22); err != nil {
		t.Fatal(err)
	}
}

func TestDetectSSHPortFromListenerWithoutSSHEnvironment(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	runner := &fakeRunner{responses: map[string][]byte{"ss -H -ltnp": []byte("LISTEN 0 128 0.0.0.0:10050 0.0.0.0:* users:((\"zabbix_agentd\",pid=2,fd=4))\nLISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:((\"sshd\",pid=1,fd=3))\nLISTEN 0 128 [::]:22 [::]:* users:((\"sshd\",pid=1,fd=4))\n")}}
	port, err := detectSSHPort(context.Background(), runner, 0)
	if err != nil {
		t.Fatal(err)
	}
	if port != 22 {
		t.Fatalf("port = %d, want 22", port)
	}
}

func TestDetectSSHPortRejectsAmbiguousListenersWithoutExplicitPort(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	runner := &fakeRunner{responses: map[string][]byte{"ss -H -ltnp": []byte("LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:((\"sshd\",pid=1,fd=3))\nLISTEN 0 128 0.0.0.0:2222 0.0.0.0:* users:((\"sshd\",pid=1,fd=4))\n")}}
	if _, err := detectSSHPort(context.Background(), runner, 0); err == nil || !strings.Contains(err.Error(), "несколько SSH-портов") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifySSHListenerAcceptsExplicitListenerWithoutSSHEnvironment(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	runner := &fakeRunner{responses: map[string][]byte{"ss -H -ltnp": []byte("LISTEN 0 128 0.0.0.0:2222 0.0.0.0:* users:((\"sshd\",pid=1,fd=3))\n")}}
	if err := verifySSHListener(context.Background(), runner, 2222); err != nil {
		t.Fatal(err)
	}
}

func TestFirewallPlanRecordsExactOwnedComments(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{"ufw status": []byte("22/tcp ALLOW Anywhere\n")}}
	rules, err := (Firewall{Runner: runner}).Plan(context.Background(), 22, 443)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"443/tcp|vpnctl-vless"}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules = %#v, want %#v", rules, want)
	}
}

func TestFirewallRemoveDoesNotDeleteSamePortWithoutOwnedComment(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"ufw status":                []byte("443/tcp ALLOW Anywhere # admin-rule\n"),
		"ufw default deny incoming": nil,
		"ufw --force disable":       nil,
	}}
	err := (Firewall{Runner: runner}).RemoveRules(context.Background(), []string{"443/tcp|vpnctl-vless"}, FirewallSnapshot{DefaultIncoming: "deny"})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if len(call.Args) > 1 && call.Args[1] == "delete" {
			t.Fatalf("deleted an unmanaged rule: %#v", call)
		}
	}
}
