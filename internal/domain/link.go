package domain

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ClientLink is the VLESS Reality URI handed to a VPN user, parsed into the
// fields a client needs. Every field here is something a broken value of which
// makes the tunnel connect but carry no traffic, so each one is validated.
type ClientLink struct {
	Raw         string
	UUID        string
	Address     string
	Port        int
	Security    string
	Network     string
	Flow        string
	SNI         string
	PublicKey   string
	ShortID     string
	Fingerprint string
	SpiderX     string
	Label       string
}

// DefaultFingerprint is the uTLS profile vpnctl puts in every link it hands
// out. Every profile in SupportedFingerprints passes over a real network path,
// so this is not a correctness choice between them — it is the one with
// positive evidence from an actual handset: on v2rayNG over a mobile carrier a
// link with "chrome" connected and carried no traffic, and the same link with
// "safari" worked at once. Reality answers a handshake it cannot authenticate
// by relaying the client to its decoy site, which is why that failure looks
// like a healthy connection.
//
// No test available here reproduces it — mobile carrier paths are not
// something a VPS can dial — so treat the default as the best available
// starting point, not as proof, and hand a user another profile when their app
// connects without carrying traffic.
const DefaultFingerprint = "safari"

// VisionFlow is the only flow a vpnctl server accepts. vpnctl provisions every
// client with it, and a link that omits it carries no traffic on any client
// core tested — 1.8.24 through 26.7.11 and sing-box alike — while still
// connecting. It is pinned rather than passed through for that reason.
const VisionFlow = "xtls-rprx-vision"

// ShareTransport is the transport name written into share links. Xray renamed
// this transport to "raw" in 25.x but kept "tcp" as an alias, and cores older
// than that only understand "tcp", so "tcp" is the value every client reads.
const ShareTransport = "tcp"

// SupportedFingerprints are the uTLS profiles vpnctl will hand out, ordered
// best first. Each one was verified over a real internet path between two
// networks against Xray cores 1.8.24, 24.9.30, 25.3.6, 25.9.11 and 26.7.11 —
// the range client apps embed — plus sing-box 1.12.4.
//
// Deliberately absent: "android" and "360" fail the handshake outright, and
// "random"/"randomized" pass on some cores and fail on others (1.8.24, 25.3.6
// and 25.9.11 among them). A profile that works on four cores out of five is
// not a rescue option, it is another silent no-traffic tunnel.
var SupportedFingerprints = []string{"safari", "chrome", "firefox", "ios", "edge"}

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	realityKeyRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	shortIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{0,16}$`)
)

// ValidFingerprint reports whether a uTLS profile is one vpnctl will hand out.
func ValidFingerprint(value string) bool {
	for _, supported := range SupportedFingerprints {
		if value == supported {
			return true
		}
	}
	return false
}

// ParseClientLink validates a vless:// Reality URI. It rejects anything a
// client would silently accept and then fail to pass traffic through.
func ParseClientLink(raw string) (ClientLink, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ClientLink{}, fmt.Errorf("ссылка пуста")
	}
	if len(trimmed) > 8192 {
		return ClientLink{}, fmt.Errorf("ссылка слишком длинная")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ClientLink{}, fmt.Errorf("ссылка не разбирается как URI")
	}
	if parsed.Scheme != "vless" {
		return ClientLink{}, fmt.Errorf("ожидалась схема vless://, получена %q", parsed.Scheme)
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return ClientLink{}, fmt.Errorf("в ссылке нет идентификатора пользователя")
	}
	uuid := parsed.User.Username()
	if !uuidPattern.MatchString(uuid) {
		return ClientLink{}, fmt.Errorf("идентификатор пользователя не является UUID")
	}
	host, rawPort, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return ClientLink{}, fmt.Errorf("в ссылке нет адреса и порта сервера")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return ClientLink{}, fmt.Errorf("некорректный порт сервера")
	}
	if !validHost(host) {
		return ClientLink{}, fmt.Errorf("некорректный адрес сервера")
	}

	query := parsed.Query()
	link := ClientLink{
		Raw:         trimmed,
		UUID:        uuid,
		Address:     host,
		Port:        port,
		Security:    query.Get("security"),
		Network:     query.Get("type"),
		Flow:        query.Get("flow"),
		SNI:         query.Get("sni"),
		PublicKey:   query.Get("pbk"),
		ShortID:     query.Get("sid"),
		Fingerprint: query.Get("fp"),
		SpiderX:     query.Get("spx"),
		Label:       parsed.Fragment,
	}
	if link.Security != "reality" {
		return ClientLink{}, fmt.Errorf("ожидался security=reality, получено %q", link.Security)
	}
	if link.Network == "" {
		link.Network = "tcp"
	}
	if link.Network != "tcp" && link.Network != "raw" {
		return ClientLink{}, fmt.Errorf("неподдерживаемый транспорт %q", link.Network)
	}
	if !validHost(link.SNI) {
		return ClientLink{}, fmt.Errorf("некорректный sni в ссылке")
	}
	if !realityKeyRe.MatchString(link.PublicKey) {
		return ClientLink{}, fmt.Errorf("некорректный публичный ключ Reality (pbk)")
	}
	if !shortIDPattern.MatchString(link.ShortID) || len(link.ShortID)%2 != 0 {
		return ClientLink{}, fmt.Errorf("некорректный shortId (sid) в ссылке")
	}
	if link.Flow != VisionFlow {
		return ClientLink{}, fmt.Errorf("в ссылке нет flow=%s, без него туннель подключается, но не передаёт трафик", VisionFlow)
	}
	if link.Fingerprint == "" {
		link.Fingerprint = DefaultFingerprint
	}
	if !ValidFingerprint(link.Fingerprint) {
		return ClientLink{}, fmt.Errorf("fingerprint %q не проходит рукопожатие Reality", link.Fingerprint)
	}
	if link.SpiderX == "" {
		link.SpiderX = "/"
	}
	return link, nil
}

// PublicClientLink turns the URI 3x-ui generates into the exact link vpnctl
// hands a user. 3x-ui fills the client-side fields from its own database and
// randomises spiderX on every call, so without this the link a user receives
// drifts from anything vpnctl ever verified. Every field pinned here has the
// same failure mode when it drifts: the tunnel connects and carries no
// traffic, which no amount of staring at the client will explain. An empty
// fingerprint selects DefaultFingerprint.
func PublicClientLink(raw, address string, port int, fingerprint string) (string, error) {
	if fingerprint == "" {
		fingerprint = DefaultFingerprint
	}
	if !ValidFingerprint(fingerprint) {
		return "", fmt.Errorf("неизвестный fingerprint %q, доступны: %s", fingerprint, strings.Join(SupportedFingerprints, ", "))
	}
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "vless" || link.User == nil || link.User.Username() == "" || link.User.String() != link.User.Username() {
		return "", fmt.Errorf("некорректный VLESS URI")
	}
	if net.ParseIP(address) == nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("некорректный публичный адрес сервера")
	}
	link.Host = net.JoinHostPort(address, strconv.Itoa(port))
	link.Path = ""
	query := link.Query()
	query.Set("encryption", "none")
	query.Set("fp", fingerprint)
	query.Set("spx", "/")
	query.Set("flow", VisionFlow)
	query.Set("type", ShareTransport)
	link.RawQuery = query.Encode()
	return link.String(), nil
}

// TransportNetwork maps the share-link transport name onto the name the
// bundled Xray core expects in streamSettings.
func (l ClientLink) TransportNetwork() string {
	if l.Network == "tcp" || l.Network == "" {
		return "raw"
	}
	return l.Network
}

// MatchesState reports whether the link points at the endpoint vpnctl manages.
// A link that survives parsing but points elsewhere connects and carries no
// traffic, which is the failure this guards against.
func (l ClientLink) MatchesState(s State) error {
	if l.Address != s.PublicAddress {
		return fmt.Errorf("адрес в ссылке %s не совпадает с адресом сервера %s", l.Address, s.PublicAddress)
	}
	if l.Port != s.ListenPort {
		return fmt.Errorf("порт в ссылке %d не совпадает с портом сервера %d", l.Port, s.ListenPort)
	}
	if s.RealitySNI != "" && l.SNI != s.RealitySNI {
		return fmt.Errorf("sni в ссылке %s не совпадает с настройкой сервера %s", l.SNI, s.RealitySNI)
	}
	return nil
}
