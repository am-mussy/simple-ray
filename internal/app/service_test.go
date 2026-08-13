package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
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
	if _, _, err := service.UserLink(context.Background(), "alice"); err == nil {
		t.Fatal("expected empty link response to fail")
	}
}

func TestPublicClientLinkUsesManagedEndpointAndCompatibleEncryption(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789abc@localhost:443?flow=xtls-rprx-vision&security=reality&pbk=public-key&sid=abcdef0123456789&spx=/path&type=tcp#vpn"
	result, err := publicClientLink(raw, "203.0.113.10", 8443)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "203.0.113.10:8443" {
		t.Fatalf("host = %q", parsed.Host)
	}
	if parsed.Query().Get("encryption") != "none" || parsed.Query().Get("pbk") != "public-key" || parsed.Query().Get("spx") != "/path" {
		t.Fatalf("query = %q", parsed.RawQuery)
	}
}

func TestPublicClientLinkFormatsIPv6(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789abc@localhost:443?security=reality"
	result, err := publicClientLink(raw, "2001:db8::1", 443)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "[2001:db8::1]:443" {
		t.Fatalf("host = %q", parsed.Host)
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
	service := New(store, filepath.Join(t.TempDir(), "lock"))
	service.APIFactory = func(domain.State, domain.Secrets) (API, error) { return unavailableGetAPI{}, nil }
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "remove", run: func() error { _, err := service.RemoveUser(context.Background(), "alice"); return err }},
		{name: "show", run: func() error { _, _, err := service.UserLink(context.Background(), "alice"); return err }},
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
