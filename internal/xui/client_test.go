package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientLinksRejectsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("authorization header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":"","obj":[]}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL+"/base", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClientLinks(context.Background(), "alice"); err == nil {
		t.Fatal("expected empty link response to fail")
	}
}

func TestClientLinksRejectsTerminalControlsAndMalformedURIs(t *testing.T) {
	tests := []string{
		"vless://abc\x1b]0;owned\x07",
		"vless://",
		"vless://uuid-without-host",
		"vless://uuid@example.com:443/ path",
	}
	for _, link := range tests {
		t.Run(link, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				payload, marshalErr := json.Marshal(map[string]any{"success": true, "msg": "", "obj": []string{link}})
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ClientLinks(context.Background(), "alice"); err == nil {
				t.Fatalf("accepted unsafe VLESS URI %q", link)
			}
		})
	}
}

func TestAPIEnvelopeFailureOnHTTP200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"msg":"not found","obj":null}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetClient(context.Background(), "alice"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetClientTreatsNullObjectAsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":"","obj":null}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetClient(context.Background(), "alice"); err == nil {
		t.Fatal("expected null client to be treated as not found")
	}
}

func TestImportDatabaseRejectsOversizeBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	source := io.MultiReader(bytes.NewReader(make([]byte, 1<<20)), io.LimitReader(zeroReader{}, 128<<20))
	if err := client.ImportDatabase(context.Background(), "database.db", source); err == nil {
		t.Fatal("expected oversized import to fail")
	}
	if requests.Load() != 0 {
		t.Fatal("oversized database was sent to the API")
	}
}

func TestDeleteClientRejectsTrailingResponseGarbage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":""} attacker-controlled trailing bytes`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteClient(context.Background(), "alice"); err == nil {
		t.Fatal("delete accepted a malformed response with trailing data")
	}
}

func TestDeleteClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":"`+strings.Repeat("x", maxJSONResponse)+`"}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteClient(context.Background(), "alice"); err == nil {
		t.Fatal("delete accepted a response larger than the API limit")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
