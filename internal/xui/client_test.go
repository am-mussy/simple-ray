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

func TestGetClientDecodesV350DetailResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":"","obj":{"client":{"id":9,"uuid":"uuid","email":"alice","enable":true,"flow":"xtls-rprx-vision"},"inboundIds":[17]}}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	record, err := client.GetClient(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if record.RecordID != 9 || record.ID != "uuid" || len(record.InboundIDs) != 1 || record.InboundIDs[0] != 17 {
		t.Fatalf("record = %#v", record)
	}
}

func TestAddClientUsesV350PayloadClientID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		var client map[string]json.RawMessage
		if err := json.Unmarshal(payload["client"], &client); err != nil {
			t.Fatal(err)
		}
		if string(client["id"]) != `"uuid"` {
			t.Fatalf("client payload = %s", payload["client"])
		}
		if _, exists := client["uuid"]; exists {
			t.Fatal("v3.5.0 create payload must use id, not uuid")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":"","obj":{}}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AddClient(context.Background(), ClientCreate{Client: ClientRecord{ID: "uuid", Email: "alice", Enable: true}, InboundIDs: []int64{17}}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusDecodesV350NestedXrayState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"msg":"","obj":{"cpu":1.5,"xray":{"state":"running","version":"25.8.3"}}}`)
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.XrayRunning() || status.Xray.Version != "25.8.3" {
		t.Fatalf("status = %#v", status)
	}
}

func TestSettingsUseV350EndpointsAndPreservePayload(t *testing.T) {
	var update map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/panel/api/setting/all":
			if r.Method != http.MethodPost {
				t.Fatalf("all method = %s", r.Method)
			}
			io.WriteString(w, `{"success":true,"msg":"","obj":{"subEnable":true,"subListen":""}}`)
		case "/panel/api/setting/update":
			if r.Method != http.MethodPost {
				t.Fatalf("update method = %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Fatal(err)
			}
			io.WriteString(w, `{"success":true,"msg":"","obj":null}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewWithHTTPClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := client.AllSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings["subEnable"] = false
	settings["subListen"] = "127.0.0.1"
	if err := client.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if update["subEnable"] != false || update["subListen"] != "127.0.0.1" {
		t.Fatalf("update = %#v", update)
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
