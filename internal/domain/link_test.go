package domain

import (
	"net/url"
	"testing"
)

const goodLink = "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?" +
	"encryption=none&flow=xtls-rprx-vision&fp=chrome&" +
	"pbk=cTwKVesuotkAtmlKvQGGFqCfK9sL-8OaHHsLAbapxFQ&security=reality&" +
	"sid=d38043ca18700534&sni=www.cloudflare.com&spx=%2Fabc&type=tcp#label"

func TestParseClientLinkReadsEveryConnectionParameter(t *testing.T) {
	link, err := ParseClientLink(goodLink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if link.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("uuid = %q", link.UUID)
	}
	if link.Address != "203.0.113.10" || link.Port != 443 {
		t.Fatalf("endpoint = %s:%d", link.Address, link.Port)
	}
	if link.SNI != "www.cloudflare.com" || link.ShortID != "d38043ca18700534" {
		t.Fatalf("reality = %q %q", link.SNI, link.ShortID)
	}
	if link.Flow != "xtls-rprx-vision" || link.Fingerprint != "chrome" || link.SpiderX != "/abc" {
		t.Fatalf("client = %q %q %q", link.Flow, link.Fingerprint, link.SpiderX)
	}
	// Share links say "tcp"; the bundled Xray core calls the same transport "raw".
	if link.TransportNetwork() != "raw" {
		t.Fatalf("transport = %q", link.TransportNetwork())
	}
}

func TestParseClientLinkRejectsBrokenLinks(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "wrong scheme", raw: "vmess://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality"},
		{name: "no uuid", raw: "vless://@203.0.113.10:443?security=reality"},
		{name: "uuid not a uuid", raw: "vless://not-a-uuid@203.0.113.10:443?security=reality"},
		{name: "no port", raw: "vless://11111111-2222-3333-4444-555555555555@203.0.113.10?security=reality"},
		{name: "not reality", raw: "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=tls"},
		{name: "missing public key", raw: "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality&sni=a.com"},
		{name: "short public key", raw: "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality&sni=a.com&pbk=tooshort"},
		{name: "odd shortid", raw: "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality&sni=a.com&pbk=cTwKVesuotkAtmlKvQGGFqCfK9sL-8OaHHsLAbapxFQ&sid=abc"},
		{name: "unknown flow", raw: "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality&sni=a.com&pbk=cTwKVesuotkAtmlKvQGGFqCfK9sL-8OaHHsLAbapxFQ&flow=nonsense"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseClientLink(test.raw); err == nil {
				t.Fatal("broken link was accepted")
			}
		})
	}
}

func TestPublicClientLinkUsesManagedEndpointAndPinsClientProfile(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789abc@localhost:443?flow=xtls-rprx-vision&security=reality&pbk=public-key&sid=abcdef0123456789&fp=android&spx=/845bd8cfe&type=tcp#vpn"
	result, err := PublicClientLink(raw, "203.0.113.10", 8443, "")
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
	query := parsed.Query()
	if query.Get("encryption") != "none" || query.Get("pbk") != "public-key" {
		t.Fatalf("query = %q", parsed.RawQuery)
	}
	// 3x-ui stores its own fingerprint and randomises spiderX per call. Passing
	// either through is how a user ends up with a tunnel that connects and
	// carries nothing.
	if query.Get("fp") != DefaultFingerprint {
		t.Fatalf("fingerprint = %q, want %q", query.Get("fp"), DefaultFingerprint)
	}
	if query.Get("spx") != "/" {
		t.Fatalf("spiderX = %q, want /", query.Get("spx"))
	}
	if query.Get("flow") != VisionFlow {
		t.Fatalf("flow = %q, want %q", query.Get("flow"), VisionFlow)
	}
	if query.Get("type") != ShareTransport {
		t.Fatalf("transport = %q, want %q", query.Get("type"), ShareTransport)
	}
}

// A link 3x-ui returns without a flow connects on every client core and moves
// no bytes, so the canonical link must supply one rather than pass the gap on.
func TestPublicClientLinkSuppliesFlowAndTransportWhenTheyAreMissing(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789abc@localhost:443?security=reality&pbk=public-key"
	result, err := PublicClientLink(raw, "203.0.113.10", 443, "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("flow") != VisionFlow || parsed.Query().Get("type") != ShareTransport {
		t.Fatalf("query = %q", parsed.RawQuery)
	}
}

func TestParseClientLinkRejectsMissingFlow(t *testing.T) {
	raw := "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?" +
		"encryption=none&fp=safari&pbk=cTwKVesuotkAtmlKvQGGFqCfK9sL-8OaHHsLAbapxFQ&" +
		"security=reality&sid=d38043ca18700534&sni=www.cloudflare.com&type=tcp"
	if _, err := ParseClientLink(raw); err == nil {
		t.Fatal("link without flow was accepted")
	}
}

func TestPublicClientLinkHonoursRescueFingerprintAndRejectsUnknownOnes(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789abc@localhost:443?security=reality"
	result, err := PublicClientLink(raw, "203.0.113.10", 443, "chrome")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("fp") != "chrome" {
		t.Fatalf("fingerprint = %q, want chrome", parsed.Query().Get("fp"))
	}
	for _, rejected := range []string{"android", "360", "nonsense"} {
		if _, err := PublicClientLink(raw, "203.0.113.10", 443, rejected); err == nil {
			t.Fatalf("fingerprint %q was accepted", rejected)
		}
	}
}

func TestPublicClientLinkFormatsIPv6(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789abc@localhost:443?security=reality"
	result, err := PublicClientLink(raw, "2001:db8::1", 443, "")
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

func TestClientLinkMatchesStateCatchesWrongEndpoint(t *testing.T) {
	state := State{PublicAddress: "203.0.113.10", ListenPort: 443, RealitySNI: "www.cloudflare.com"}
	link, err := ParseClientLink(goodLink)
	if err != nil {
		t.Fatal(err)
	}
	if err := link.MatchesState(state); err != nil {
		t.Fatalf("matching link rejected: %v", err)
	}

	wrongPort := link
	wrongPort.Port = 8443
	if err := wrongPort.MatchesState(state); err == nil {
		t.Fatal("link on the wrong port was accepted")
	}
	wrongAddress := link
	wrongAddress.Address = "198.51.100.7"
	if err := wrongAddress.MatchesState(state); err == nil {
		t.Fatal("link to the wrong server was accepted")
	}
	// The exact failure seen in the field: a profile kept an SNI from an
	// earlier install, so Reality relayed the client to its decoy site.
	staleSNI := link
	staleSNI.SNI = "www.microsoft.com"
	if err := staleSNI.MatchesState(state); err == nil {
		t.Fatal("link with a stale SNI was accepted")
	}
}
