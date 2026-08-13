package domain

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	SchemaVersion = 1
	XUIVersion    = "v3.5.0"
)

var ProductVersion = "0.1.0-dev"

var userNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
var hostNamePattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)
var basePathPattern = regexp.MustCompile(`^/[A-Za-z0-9_-]{8,64}/?$`)

type State struct {
	SchemaVersion      int       `json:"schemaVersion"`
	VPNCTLVersion      string    `json:"vpnctlVersion"`
	XUIVersion         string    `json:"xuiVersion"`
	Architecture       string    `json:"architecture"`
	ServerName         string    `json:"serverName"`
	PublicAddress      string    `json:"publicAddress"`
	InboundID          int64     `json:"inboundId"`
	InboundRemark      string    `json:"inboundRemark"`
	ListenPort         int       `json:"listenPort"`
	PanelPort          int       `json:"panelPort"`
	PanelBasePath      string    `json:"panelBasePath"`
	PanelListen        string    `json:"panelListen"`
	RealityTarget      string    `json:"realityTarget"`
	RealitySNI         string    `json:"realitySni"`
	FirewallRules      []string  `json:"firewallRules,omitempty"`
	InstallID          string    `json:"installId,omitempty"`
	OwnedProgramDir    string    `json:"ownedProgramDir,omitempty"`
	OwnedConfigDir     string    `json:"ownedConfigDir,omitempty"`
	OwnedLogDir        string    `json:"ownedLogDir,omitempty"`
	OwnedServiceUnit   string    `json:"ownedServiceUnit,omitempty"`
	OwnedServiceUser   string    `json:"ownedServiceUser,omitempty"`
	ServiceUnitSHA256  string    `json:"serviceUnitSha256,omitempty"`
	SSHPort            int       `json:"sshPort,omitempty"`
	FirewallWasActive  bool      `json:"firewallWasActive,omitempty"`
	FirewallIncoming   string    `json:"firewallIncoming,omitempty"`
	FirewallPostSHA256 string    `json:"firewallPostSha256,omitempty"`
	InstallationPhase  string    `json:"installationPhase"`
	LastBackup         string    `json:"lastBackup,omitempty"`
	InstalledAt        time.Time `json:"installedAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type Secrets struct {
	SchemaVersion int    `json:"schemaVersion"`
	APIToken      string `json:"apiToken"`
}

type User struct {
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
	ExpiryTime int64     `json:"expiryTime,omitempty"`
}

type Check struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func ValidateUserName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if !userNamePattern.MatchString(name) {
		return "", fmt.Errorf("name must contain 1-32 letters, numbers, underscores, or hyphens")
	}
	return name, nil
}

func ValidateState(s State) error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported state schema %d", s.SchemaVersion)
	}
	if s.PanelListen != "127.0.0.1" {
		return fmt.Errorf("panel listen address must be 127.0.0.1")
	}
	if s.Architecture != "amd64" && s.Architecture != "arm64" {
		return fmt.Errorf("unsupported architecture %q", s.Architecture)
	}
	if net.ParseIP(s.PublicAddress) == nil {
		return fmt.Errorf("public address must be an IP address")
	}
	if s.PanelPort < 1 || s.PanelPort > 1023 || s.ListenPort < 1 || s.ListenPort > 65535 {
		return fmt.Errorf("invalid port in state")
	}
	if !basePathPattern.MatchString(s.PanelBasePath) {
		return fmt.Errorf("invalid panel base path")
	}
	targetHost, targetPort, err := net.SplitHostPort(s.RealityTarget)
	if err != nil || targetPort != "443" || !validHost(targetHost) || !validHost(s.RealitySNI) {
		return fmt.Errorf("invalid Reality target or SNI")
	}
	if s.InboundID < 1 || s.InboundRemark == "" || s.PublicAddress == "" {
		return fmt.Errorf("state is incomplete")
	}
	return nil
}

func validHost(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	if strings.ContainsAny(value, "\x00\r\n\t /\\") || !hostNamePattern.MatchString(value) {
		return false
	}
	parsed, err := url.Parse("https://" + value)
	return err == nil && parsed.Hostname() == value
}

type Error struct {
	Code     string
	Message  string
	Hint     string
	ExitCode int
	Cause    error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func E(code, message, hint string, exit int, cause error) error {
	return &Error{Code: code, Message: message, Hint: hint, ExitCode: exit, Cause: cause}
}
