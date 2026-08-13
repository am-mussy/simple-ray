package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/mussy/simple-ray/internal/app"
	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/state"
	"github.com/mussy/simple-ray/internal/xui"
)

func TestStatusReturnsDegradedExitCodeWhenXrayIsStopped(t *testing.T) {
	store := state.New(t.TempDir())
	installation := domain.State{
		SchemaVersion: domain.SchemaVersion,
		VPNCTLVersion: domain.ProductVersion,
		XUIVersion:    domain.XUIVersion,
		Architecture:  "amd64",
		PublicAddress: "203.0.113.1",
		InboundID:     1,
		InboundRemark: "vpnctl-vless-reality",
		ListenPort:    443,
		PanelPort:     853,
		PanelBasePath: "/adminpath",
		PanelListen:   "127.0.0.1",
		RealityTarget: "example.com:443",
		RealitySNI:    "example.com",
	}
	if err := store.SaveState(installation); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecrets(domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: "token"}); err != nil {
		t.Fatal(err)
	}
	service := app.New(store, filepath.Join(t.TempDir(), "lock"))
	service.APIFactory = func(domain.State, domain.Secrets) (app.API, error) { return stoppedXrayAPI{}, nil }
	var stdout, stderr bytes.Buffer
	command := CLI{Service: service, Out: &stdout, Err: &stderr}
	if exit := command.Run(context.Background(), []string{"status"}); exit != 5 {
		t.Fatalf("exit = %d, want 5; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("DEGRADED")) {
		t.Fatalf("degraded status is missing: %q", stdout.String())
	}
}

type stoppedXrayAPI struct{}

func (stoppedXrayAPI) ListClients(context.Context) ([]xui.ClientRecord, error) { return nil, nil }
func (stoppedXrayAPI) GetClient(context.Context, string) (xui.ClientRecord, error) {
	return xui.ClientRecord{}, nil
}
func (stoppedXrayAPI) AddClient(context.Context, xui.ClientCreate) error     { return nil }
func (stoppedXrayAPI) DeleteClient(context.Context, string) error            { return nil }
func (stoppedXrayAPI) ClientLinks(context.Context, string) ([]string, error) { return nil, nil }
func (stoppedXrayAPI) Status(context.Context) (xui.ServerStatus, error) {
	return xui.ServerStatus{XrayState: false}, nil
}
func (stoppedXrayAPI) GetDatabase(context.Context, io.Writer, int64) error     { return nil }
func (stoppedXrayAPI) ImportDatabase(context.Context, string, io.Reader) error { return nil }
