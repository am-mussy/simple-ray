//go:build linux

package panelbootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostJSONSendsCSRFAndRejectsFailureEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CSRF-Token") != "csrf" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("required bootstrap headers are missing")
		}
		_, _ = io.WriteString(w, `{"success":false,"msg":"no"}`)
	}))
	defer server.Close()
	if err := postJSON(context.Background(), server.Client(), server.URL, "csrf", map[string]string{"value": "secret"}); err == nil {
		t.Fatal("failure envelope was accepted")
	}
}
