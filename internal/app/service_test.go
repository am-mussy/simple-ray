package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/state"
	"github.com/mussy/simple-ray/internal/xui"
)

func TestUserLinkEmptyResultDoesNotPanic(t *testing.T) {
	store := state.New(t.TempDir())
	installation := validState()
	if err := store.SaveState(installation); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecrets(domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: "token"}); err != nil {
		t.Fatal(err)
	}
	service := New(store, filepath.Join(t.TempDir(), "lock"))
	service.APIFactory = func(domain.State, domain.Secrets) (API, error) { return emptyLinksAPI{}, nil }
	if _, _, err := service.UserLink(context.Background(), "alice", ""); err == nil {
		t.Fatal("expected empty link response to fail")
	}
}

func TestUserLookupDoesNotMisreportAPIOutageAsNotFound(t *testing.T) {
	store := state.New(t.TempDir())
	if err := store.SaveState(validState()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecrets(domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: "token"}); err != nil {
		t.Fatal(err)
	}
	// Point at a directory vpnctl creates itself: t.TempDir() applies the
	// process umask, so its own permissions are not the 0700 the lock demands.
	service := New(store, filepath.Join(t.TempDir(), "runtime", "lock"))
	service.APIFactory = func(domain.State, domain.Secrets) (API, error) { return unavailableGetAPI{}, nil }
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "remove", run: func() error { _, err := service.RemoveUser(context.Background(), "alice"); return err }},
		{name: "show", run: func() error { _, _, err := service.UserLink(context.Background(), "alice", ""); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var domainError *domain.Error
			if err := test.run(); !errors.As(err, &domainError) || domainError.Code != "API_UNAVAILABLE" {
				t.Fatalf("error = %#v, want API_UNAVAILABLE", err)
			}
		})
	}
}

type emptyLinksAPI struct{}

type unavailableGetAPI struct{ emptyLinksAPI }

func (unavailableGetAPI) GetClient(context.Context, string) (xui.ClientRecord, error) {
	return xui.ClientRecord{}, &xui.APIError{Status: http.StatusInternalServerError, Message: "Internal Server Error"}
}

func (emptyLinksAPI) ListClients(context.Context) ([]xui.ClientRecord, error) { return nil, nil }
func (emptyLinksAPI) GetClient(context.Context, string) (xui.ClientRecord, error) {
	return xui.ClientRecord{Email: "alice", Enable: true}, nil
}
func (emptyLinksAPI) AddClient(context.Context, xui.ClientCreate) error     { return nil }
func (emptyLinksAPI) DeleteClient(context.Context, string) error            { return nil }
func (emptyLinksAPI) ClientLinks(context.Context, string) ([]string, error) { return nil, nil }
func (emptyLinksAPI) Status(context.Context) (xui.ServerStatus, error) {
	return xui.ServerStatus{}, nil
}
func (emptyLinksAPI) GetDatabase(context.Context, io.Writer, int64) error     { return nil }
func (emptyLinksAPI) ImportDatabase(context.Context, string, io.Reader) error { return nil }

func validState() domain.State {
	return domain.State{
		SchemaVersion: domain.SchemaVersion, VPNCTLVersion: domain.ProductVersion, XUIVersion: domain.XUIVersion,
		Architecture: "amd64", PublicAddress: "203.0.113.1", InboundID: 1, InboundRemark: "vpnctl-vless-reality",
		ListenPort: 443, PanelPort: 853, PanelBasePath: "/adminpath", PanelListen: "127.0.0.1", RealityTarget: "example.com:443", RealitySNI: "example.com",
	}
}
