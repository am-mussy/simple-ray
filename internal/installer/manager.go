//go:build linux

package installer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/state"
	"github.com/mussy/simple-ray/internal/xui"
)

const (
	programDir    = "/usr/local/x-ui"
	configDir     = "/etc/x-ui"
	logDir        = "/var/log/x-ui"
	serviceUnit   = "/etc/systemd/system/x-ui.service"
	managedRemark = "vpnctl-vless-reality"
	serviceUser   = "vpnctl-xui"

	// realityMinClientVer accepts VPN clients of any age. Xray-core 26.7.11
	// defaults this to 26.3.27, which silently rejects every mainstream mobile
	// client: the connection still establishes because REALITY relays rejected
	// clients to its decoy site, so the user sees a connected VPN that carries
	// no traffic. An empty string is not enough, it selects the default.
	realityMinClientVer = "0.0.0"
)

type Request struct {
	User          string
	ServerName    string
	PublicAddress string
	ListenPort    int
	SSHPort       int
	RealitySNI    string
	RealityTarget string
}

type Result struct {
	State    domain.State `json:"state"`
	User     domain.User  `json:"user"`
	Existing bool         `json:"existing"`
}

type Manager struct {
	Preflight  *Installer
	Store      *state.Store
	Runner     Runner
	Firewall   Firewall
	Executable string
	NewAPI     func(string, string) (*xui.Client, error)
}

type journal struct {
	InstallID     string           `json:"installId"`
	StoreCreated  bool             `json:"storeCreated"`
	Program       bool             `json:"program"`
	Config        bool             `json:"config"`
	Log           bool             `json:"log"`
	Unit          bool             `json:"unit"`
	User          bool             `json:"user"`
	FirewallRules []string         `json:"firewallRules"`
	Firewall      FirewallSnapshot `json:"firewall"`
	SSHPort       int              `json:"sshPort"`
	VPNPort       int              `json:"vpnPort"`
	StageRoot     string           `json:"stageRoot,omitempty"`
}

func NewManager(store *state.Store) *Manager {
	executable, _ := os.Executable()
	runner := ExecRunner{}
	return &Manager{Preflight: New(), Store: store, Runner: runner, Firewall: Firewall{Runner: runner}, Executable: executable, NewAPI: xui.New}
}

func (m *Manager) Install(ctx context.Context, request Request) (result Result, returned error) {
	name, err := domain.ValidateUserName(request.User)
	if err != nil {
		return result, err
	}
	if _, journalErr := os.Lstat(m.journalPath()); journalErr == nil {
		if err := m.recoverInterrupted(ctx); err != nil {
			return result, err
		}
	} else if !errors.Is(journalErr, os.ErrNotExist) {
		return result, journalErr
	}
	check, err := m.Preflight.Check()
	if err != nil {
		return result, err
	}
	if check.StateExists {
		installed, err := m.Store.LoadState()
		if err != nil {
			return result, err
		}
		if installed.XUIVersion != Version || installed.InboundRemark != managedRemark || installed.OwnedProgramDir != programDir || installed.OwnedConfigDir != configDir || installed.OwnedLogDir != logDir || installed.OwnedServiceUnit != serviceUnit || installed.OwnedServiceUser != serviceUser {
			return result, errors.New("existing installation is incompatible")
		}
		if err := verifyMarker(programDir, installed.InstallID); err != nil {
			return result, err
		}
		if err := verifyMarker(configDir, installed.InstallID); err != nil {
			return result, err
		}
		if err := verifyMarker(logDir, installed.InstallID); err != nil {
			return result, err
		}
		unitData, err := os.ReadFile(serviceUnit)
		if err != nil {
			return result, err
		}
		sum := sha256.Sum256(unitData)
		if hex.EncodeToString(sum[:]) != installed.ServiceUnitSHA256 {
			return result, errors.New("managed service unit was modified")
		}
		if exists, lookupErr := serviceAccountExists(); lookupErr != nil || !exists {
			return result, errors.New("managed service account is missing or unreadable")
		}
		return Result{State: installed, User: domain.User{Name: name, Enabled: true}, Existing: true}, nil
	}
	storeExists := false
	if _, err := os.Lstat(m.Store.Dir); err == nil {
		if err := verifyReusableStore(m.Store); err != nil {
			return result, err
		}
		storeExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if _, err := os.Lstat(logDir); err == nil {
		return result, errors.New("unmanaged 3x-ui log directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if request.ListenPort == 0 {
		request.ListenPort = 443
	}
	if request.RealitySNI == "" {
		request.RealitySNI = "www.cloudflare.com"
	}
	if request.RealityTarget == "" {
		request.RealityTarget = request.RealitySNI + ":443"
	}
	if request.ServerName == "" {
		request.ServerName, _ = os.Hostname()
	}
	if net.ParseIP(request.PublicAddress) == nil {
		return result, errors.New("public address must be an IP address")
	}
	if request.ListenPort < 1 || request.ListenPort > 65535 {
		return result, errors.New("VPN port is invalid")
	}
	request.SSHPort, err = detectSSHPort(ctx, m.Runner, request.SSHPort)
	if err != nil {
		return result, err
	}
	if err := verifySSHListener(ctx, m.Runner, request.SSHPort); err != nil {
		return result, err
	}
	if request.SSHPort == request.ListenPort {
		return result, errors.New("SSH and VPN ports must differ")
	}
	if exists, lookupErr := serviceAccountExists(); lookupErr != nil {
		return result, lookupErr
	} else if exists {
		return result, errors.New("unmanaged service user already exists")
	}
	if err := ensurePortAvailable(request.ListenPort); err != nil {
		return result, err
	}
	available, diskErr := freeDisk(filepath.Dir(programDir))
	if diskErr != nil || available < 1<<30 {
		return result, errors.New("at least 1 GiB free disk is required")
	}
	if err := probeReality(ctx, request.RealityTarget, request.RealitySNI); err != nil {
		return result, err
	}
	if err := requiredTools(ctx, m.Runner); err != nil {
		return result, err
	}
	if err := requirePrivilegedPortBoundary("/proc/sys/net/ipv4/ip_unprivileged_port_start"); err != nil {
		return result, err
	}
	firewallSnapshot, err := m.Firewall.Preflight(ctx)
	if err != nil {
		return result, err
	}
	installID, err := randomHex(8)
	if err != nil {
		return result, err
	}
	tx := journal{InstallID: installID, StoreCreated: !storeExists, Firewall: firewallSnapshot, SSHPort: request.SSHPort, VPNPort: request.ListenPort}
	if err := m.initializeJournal(tx, storeExists); err != nil {
		return result, err
	}
	defer func() {
		if returned != nil {
			returned = errors.Join(returned, m.rollback(context.Background(), tx))
		}
	}()
	stage, stageRoot, err := m.Preflight.DownloadAndStageOwned(ctx, check.Architecture, filepath.Dir(programDir), installID)
	if err != nil {
		return result, err
	}
	tx.StageRoot = stageRoot
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	defer func() { _ = safeRemoveTreeWithin(filepath.Dir(stageRoot), stageRoot) }()
	if err := writeMarker(stage, installID); err != nil {
		return result, err
	}
	tx.Program = true
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	if err := os.Rename(stage, programDir); err != nil {
		return result, err
	}
	if err := secureProgramTree(programDir); err != nil {
		return result, err
	}
	tx.StageRoot = ""
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	username, err := randomCredential(24)
	if err != nil {
		return result, err
	}
	password, err := randomCredential(48)
	if err != nil {
		return result, err
	}
	namespace := "/run/netns/vpnctl-" + installID
	tx.Config = true
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	tx.Log = true
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	if err := createManagedDir(logDir, installID); err != nil {
		return result, err
	}
	if err := createManagedDir(configDir, installID); err != nil {
		return result, err
	}
	if err := bootstrapPanel(ctx, m.Runner, m.Executable, filepath.Join(programDir, "x-ui"), namespace, username, password); err != nil {
		return result, err
	}
	password = ""
	tx.User = true
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	if _, err := m.Runner.Run(ctx, "useradd", "--system", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", serviceUser); err != nil {
		return result, err
	}
	account, err := user.Lookup(serviceUser)
	if err != nil {
		return result, err
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	if err := secureDataTree(configDir); err != nil {
		return result, err
	}
	if err := chownTree(configDir, uid, gid); err != nil {
		return result, err
	}
	if err := os.Chown(filepath.Join(programDir, "bin"), uid, gid); err != nil {
		return result, err
	}
	if err := chownTree(logDir, uid, gid); err != nil {
		return result, err
	}
	panelPort, err := randomPrivilegedPort()
	if err != nil {
		return result, err
	}
	basePath, err := randomHex(16)
	if err != nil {
		return result, err
	}
	if output, err := m.Runner.Run(ctx, "runuser", "--user", serviceUser, "--", filepath.Join(programDir, "x-ui"), "setting", "-port", strconv.Itoa(panelPort), "-webBasePath", "/"+basePath+"/", "-listenIP", "127.0.0.1"); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return result, err
		}
		return result, fmt.Errorf("%w: %s", err, message)
	}
	unit := systemdUnit()
	if err := writeAtomicNew(serviceUnit, []byte(unit), 0644); err != nil {
		return result, err
	}
	tx.Unit = true
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	tx.FirewallRules, err = m.Firewall.Plan(ctx, request.SSHPort, request.ListenPort)
	if err != nil {
		return result, err
	}
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	if err = m.Firewall.Apply(ctx, tx.FirewallRules, request.SSHPort, firewallSnapshot); err != nil {
		return result, err
	}
	firewallSnapshot.ManagedSHA256, err = m.Firewall.Fingerprint(ctx)
	if err != nil {
		return result, err
	}
	tx.Firewall = firewallSnapshot
	if err := m.saveJournal(tx); err != nil {
		return result, err
	}
	if _, err := m.Runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return result, err
	}
	if _, err := m.Runner.Run(ctx, "systemctl", "enable", "--now", "x-ui.service"); err != nil {
		return result, err
	}
	if err := waitService(ctx, m.Runner); err != nil {
		return result, err
	}
	tokenOutput, err := m.Runner.Run(ctx, "runuser", "--user", serviceUser, "--", filepath.Join(programDir, "x-ui"), "setting", "-getApiToken", "true")
	if err != nil {
		return result, err
	}
	token, err := parseToken(string(tokenOutput))
	if err != nil {
		return result, err
	}
	api, err := m.NewAPI(fmt.Sprintf("http://127.0.0.1:%d/%s", panelPort, basePath), token)
	if err != nil {
		return result, err
	}
	if err := waitPanelAPI(ctx, api); err != nil {
		return result, err
	}
	settings, err := api.AllSettings(ctx)
	if err != nil {
		return result, err
	}
	settings["subEnable"] = false
	settings["subListen"] = "127.0.0.1"
	if err := api.UpdateSettings(ctx, settings); err != nil {
		return result, err
	}
	if _, err := m.Runner.Run(ctx, "systemctl", "restart", "x-ui.service"); err != nil {
		return result, err
	}
	if err := waitService(ctx, m.Runner); err != nil {
		return result, err
	}
	if err := waitPanelAPI(ctx, api); err != nil {
		return result, err
	}
	keys, err := api.NewX25519(ctx)
	if err != nil {
		return result, err
	}
	shortID, err := randomHex(8)
	if err != nil {
		return result, err
	}
	inbound, err := api.AddInbound(ctx, realityPayload(request, keys, shortID))
	if err != nil {
		return result, err
	}
	if inbound.ID < 1 {
		list, listErr := api.ListInbounds(ctx)
		if listErr != nil {
			return result, listErr
		}
		for _, candidate := range list {
			if candidate.Remark == managedRemark {
				inbound = candidate
			}
		}
	}
	if inbound.ID < 1 {
		return result, errors.New("managed inbound was not returned by 3x-ui")
	}
	client := xui.ClientRecord{Email: name, Enable: true, Flow: "xtls-rprx-vision"}
	if err := api.AddClient(ctx, xui.ClientCreate{Client: client, InboundIDs: []int64{inbound.ID}}); err != nil {
		return result, err
	}
	verified, err := api.GetClient(ctx, name)
	if err != nil {
		return result, err
	}
	if len(verified.InboundIDs) != 1 || verified.InboundIDs[0] != inbound.ID {
		return result, errors.New("first user attachment could not be verified")
	}
	if err := api.RestartXray(ctx); err != nil {
		return result, err
	}
	if err := waitAPIHealth(ctx, api); err != nil {
		return result, errors.New("Xray failed health check")
	}
	// Verify the link 3x-ui will actually hand this user, not one rebuilt from
	// install parameters. The two agreed until 3x-ui put its own fingerprint in
	// the URI, and the mismatch was invisible: the synthetic link passed while
	// the real one connected and carried nothing.
	clientLink, err := generatedClientLink(ctx, api, name, request.PublicAddress, request.ListenPort)
	if err != nil {
		return result, err
	}
	if err := verifyRealityTunnel(ctx, filepath.Join(programDir, "bin", "xray-linux-"+check.Architecture), clientLink); err != nil {
		return result, fmt.Errorf("Reality tunnel failed end-to-end health check: %w", err)
	}
	unitSum := sha256.Sum256([]byte(unit))
	now := time.Now().UTC()
	installed := domain.State{SchemaVersion: domain.SchemaVersion, VPNCTLVersion: domain.ProductVersion, XUIVersion: Version, Architecture: check.Architecture, ServerName: request.ServerName, PublicAddress: request.PublicAddress, InboundID: inbound.ID, InboundRemark: managedRemark, ListenPort: request.ListenPort, PanelPort: panelPort, PanelBasePath: "/" + basePath + "/", PanelListen: "127.0.0.1", RealityTarget: request.RealityTarget, RealitySNI: request.RealitySNI, FirewallRules: tx.FirewallRules, InstallationPhase: "complete", InstalledAt: now, UpdatedAt: now, InstallID: installID, OwnedProgramDir: programDir, OwnedConfigDir: configDir, OwnedLogDir: logDir, OwnedServiceUnit: serviceUnit, OwnedServiceUser: serviceUser, ServiceUnitSHA256: hex.EncodeToString(unitSum[:]), SSHPort: request.SSHPort, FirewallWasActive: firewallSnapshot.Active, FirewallIncoming: firewallSnapshot.DefaultIncoming, FirewallPostSHA256: firewallSnapshot.ManagedSHA256}
	if err := m.Store.SaveSecrets(domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: token}); err != nil {
		return result, err
	}
	if err := m.Store.SaveState(installed); err != nil {
		_ = os.Remove(m.Store.SecretsPath())
		return result, err
	}
	_ = os.Remove(m.journalPath())
	_ = os.Remove(filepath.Join(m.Store.Dir, ".vpnctl-install-owned"))
	return Result{State: installed, User: domain.User{Name: name, Enabled: true}}, nil
}

func (m *Manager) Uninstall(ctx context.Context, installed domain.State, keepData, removeBackups bool) error {
	if installed.InstallID == "" || installed.OwnedProgramDir != programDir || installed.OwnedConfigDir != configDir || installed.OwnedLogDir != logDir || installed.OwnedServiceUnit != serviceUnit || installed.OwnedServiceUser != serviceUser {
		return errors.New("ownership inventory is incomplete")
	}
	programExists, err := verifyMarkerOrMissing(programDir, installed.InstallID)
	if err != nil {
		return err
	}
	configExists, err := verifyMarkerOrMissing(configDir, installed.InstallID)
	if err != nil {
		return err
	}
	logExists, err := verifyMarkerOrMissing(logDir, installed.InstallID)
	if err != nil {
		return err
	}
	unitExists := false
	if unit, readErr := os.ReadFile(serviceUnit); readErr == nil {
		sum := sha256.Sum256(unit)
		if hex.EncodeToString(sum[:]) != installed.ServiceUnitSHA256 {
			return errors.New("service unit changed outside vpnctl")
		}
		unitExists = true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	snapshot := FirewallSnapshot{Active: installed.FirewallWasActive, DefaultIncoming: installed.FirewallIncoming, ManagedSHA256: installed.FirewallPostSHA256}
	if err := m.Firewall.VerifyManaged(ctx, snapshot); err != nil {
		return err
	}
	if unitExists {
		if err := stopManagedService(ctx, m.Runner); err != nil {
			return err
		}
	}
	if err := m.Firewall.RemoveRules(ctx, installed.FirewallRules, snapshot); err != nil {
		return err
	}
	if unitExists {
		if err := os.Remove(serviceUnit); err != nil {
			return err
		}
		if _, err := m.Runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	if programExists {
		if err := safeRemoveManagedTree(programDir, installed.InstallID); err != nil {
			return err
		}
	}
	if configExists {
		if !keepData {
			if err := safeRemoveManagedTree(configDir, installed.InstallID); err != nil {
				return err
			}
		} else {
			if err := chownTree(configDir, 0, 0); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(configDir, ".vpnctl-owned")); err != nil {
				return err
			}
		}
	}
	if logExists {
		if err := safeRemoveManagedTree(logDir, installed.InstallID); err != nil {
			return err
		}
	}
	if exists, lookupErr := serviceAccountExists(); lookupErr != nil {
		return lookupErr
	} else if exists {
		if _, err := m.Runner.Run(ctx, "userdel", serviceUser); err != nil {
			return err
		}
	}
	if removeBackups {
		info, err := os.Lstat(m.Store.BackupsDir())
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("backup directory is unsafe")
			}
			if err := safeRemoveTreeWithin(m.Store.Dir, m.Store.BackupsDir()); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(m.Store.SecretsPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(m.Store.StatePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, name := range []string{"install-transaction.json", ".vpnctl-install-owned"} {
		if err := os.Remove(filepath.Join(m.Store.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if removeBackups {
		return removeEmptyDirectory(m.Store.Dir)
	}
	return verifyReusableStore(m.Store)
}

func (m *Manager) recoverInterrupted(ctx context.Context) error {
	data, err := os.ReadFile(m.journalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var tx journal
	if json.Unmarshal(data, &tx) != nil || tx.InstallID == "" {
		return errors.New("install journal is invalid")
	}
	if installed, loadErr := m.Store.LoadState(); loadErr == nil && installed.InstallID == tx.InstallID {
		journalErr := os.Remove(m.journalPath())
		markerErr := os.Remove(filepath.Join(m.Store.Dir, ".vpnctl-install-owned"))
		if errors.Is(markerErr, os.ErrNotExist) {
			markerErr = nil
		}
		return errors.Join(journalErr, markerErr)
	}
	return m.rollback(ctx, tx)
}

func (m *Manager) rollback(ctx context.Context, tx journal) error {
	var joined error
	expectedStage := filepath.Join(filepath.Dir(programDir), ".vpnctl-install-"+tx.InstallID)
	if info, err := os.Lstat(expectedStage); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		joined = errors.Join(joined, safeRemoveTreeWithin(filepath.Dir(expectedStage), expectedStage))
	}
	serviceStopped := !tx.Unit
	if verifyMarker(programDir, tx.InstallID) == nil {
		if unitData, readErr := os.ReadFile(serviceUnit); readErr == nil {
			expected := sha256.Sum256([]byte(systemdUnit()))
			actual := sha256.Sum256(unitData)
			if actual == expected {
				if stopErr := stopManagedService(ctx, m.Runner); stopErr != nil {
					joined = errors.Join(joined, stopErr)
				} else {
					serviceStopped = true
					joined = errors.Join(joined, os.Remove(serviceUnit))
				}
			} else if tx.Unit {
				joined = errors.Join(joined, errors.New("service unit ownership could not be verified during rollback"))
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			joined = errors.Join(joined, readErr)
		}
		if tx.Unit {
			_, _ = m.Runner.Run(ctx, "systemctl", "daemon-reload")
		}
	}
	if !serviceStopped {
		return errors.Join(joined, errors.New("rollback preserved firewall and managed resources because x-ui could not be confirmed stopped"))
	}
	if len(tx.FirewallRules) > 0 {
		joined = errors.Join(joined, m.Firewall.RemoveRules(ctx, tx.FirewallRules, tx.Firewall))
	}
	if tx.Config && verifyMarker(configDir, tx.InstallID) == nil {
		joined = errors.Join(joined, safeRemoveManagedTree(configDir, tx.InstallID))
	}
	joined = errors.Join(joined, removeManagedDirStage(configDir, tx.InstallID))
	if tx.Log && verifyMarker(logDir, tx.InstallID) == nil {
		joined = errors.Join(joined, safeRemoveManagedTree(logDir, tx.InstallID))
	}
	joined = errors.Join(joined, removeManagedDirStage(logDir, tx.InstallID))
	if tx.User {
		if exists, lookupErr := serviceAccountExists(); lookupErr != nil {
			joined = errors.Join(joined, lookupErr)
		} else if exists {
			_, err := m.Runner.Run(ctx, "userdel", serviceUser)
			joined = errors.Join(joined, err)
		}
	}
	if tx.Program && verifyMarker(programDir, tx.InstallID) == nil {
		joined = errors.Join(joined, safeRemoveManagedTree(programDir, tx.InstallID))
	}
	if data, markerErr := os.ReadFile(filepath.Join(m.Store.Dir, ".vpnctl-install-owned")); markerErr == nil && strings.TrimSpace(string(data)) == tx.InstallID {
		for _, path := range []string{m.Store.StatePath(), m.Store.SecretsPath()} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				joined = errors.Join(joined, err)
			}
		}
	}
	if tx.StoreCreated {
		joined = errors.Join(joined, m.removeOwnedStore(tx.InstallID))
	} else {
		joined = errors.Join(joined, os.Remove(m.journalPath()))
		joined = errors.Join(joined, os.Remove(filepath.Join(m.Store.Dir, ".vpnctl-install-owned")))
	}
	if errors.Is(joined, os.ErrNotExist) {
		return nil
	}
	return joined
}

func stopManagedService(ctx context.Context, runner Runner) error {
	if _, err := runner.Run(ctx, "systemctl", "disable", "--now", "x-ui.service"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "systemctl", "is-active", "--quiet", "x-ui.service"); err == nil {
		return errors.New("x-ui service remained active; firewall and managed resources were preserved")
	}
	return nil
}

func (m *Manager) journalPath() string { return filepath.Join(m.Store.Dir, "install-transaction.json") }

func (m *Manager) initializeJournal(value journal, reuse bool) error {
	if reuse {
		if err := verifyReusableStore(m.Store); err != nil {
			return err
		}
		marker := filepath.Join(m.Store.Dir, ".vpnctl-install-owned")
		if err := os.WriteFile(marker, []byte(value.InstallID+"\n"), 0600); err != nil {
			return err
		}
		if err := m.saveJournal(value); err != nil {
			_ = os.Remove(marker)
			return err
		}
		return nil
	}
	parent := filepath.Dir(m.Store.Dir)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state parent directory is missing or unsafe")
	}
	temporary := filepath.Join(parent, ".vpnctl-state-"+value.InstallID)
	if err := os.Mkdir(temporary, 0700); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = safeRemoveTreeWithin(parent, temporary)
		}
	}()
	if err := os.Mkdir(filepath.Join(temporary, "backups"), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temporary, ".vpnctl-install-owned"), []byte(value.InstallID+"\n"), 0600); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := writeAtomicReplace(filepath.Join(temporary, "install-transaction.json"), data, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, m.Store.Dir); err != nil {
		return err
	}
	keep = true
	return syncDirectory(parent)
}

func (m *Manager) removeOwnedStore(installID string) error {
	data, err := os.ReadFile(filepath.Join(m.Store.Dir, ".vpnctl-install-owned"))
	if err != nil || strings.TrimSpace(string(data)) != installID {
		return errors.New("state directory ownership marker mismatch")
	}
	return safeRemoveTreeWithin(filepath.Dir(m.Store.Dir), m.Store.Dir)
}

func verifyReusableStore(store *state.Store) error {
	info, err := os.Lstat(store.Dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("existing vpnctl state directory is unsafe")
	}
	entries, err := os.ReadDir(store.Dir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "backups" || !entries[0].IsDir() {
		return errors.New("existing vpnctl state directory contains unmanaged data")
	}
	backups, err := os.Lstat(store.BackupsDir())
	if err != nil || !backups.IsDir() || backups.Mode()&os.ModeSymlink != 0 || backups.Mode().Perm()&0077 != 0 {
		return errors.New("existing vpnctl backup directory is unsafe")
	}
	return nil
}

func removeEmptyDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("state directory contains unexpected data")
	}
	return os.Remove(path)
}
func (m *Manager) saveJournal(value journal) error {
	data, _ := json.Marshal(value)
	return writeAtomicReplace(m.journalPath(), data, 0600)
}

var tokenPattern = regexp.MustCompile(`(?m)^apiToken: ([A-Za-z0-9._~-]{20,512})\r?$`)

func parseToken(output string) (string, error) {
	matches := tokenPattern.FindAllStringSubmatch(output, -1)
	if len(matches) != 1 {
		return "", errors.New("API token output was missing or ambiguous")
	}
	return matches[0][1], nil
}

// generatedClientLink pulls the URI 3x-ui produces for a user and canonicalises
// it exactly as vpnctl will when handing it out, so installation verifies the
// artifact the user receives.
func generatedClientLink(ctx context.Context, api *xui.Client, name, address string, port int) (domain.ClientLink, error) {
	links, err := api.ClientLinks(ctx, name)
	if err != nil {
		return domain.ClientLink{}, fmt.Errorf("3x-ui did not return a client link: %w", err)
	}
	if len(links) == 0 {
		return domain.ClientLink{}, errors.New("3x-ui returned no client link for the first user")
	}
	raw, err := domain.PublicClientLink(links[0], address, port, "")
	if err != nil {
		return domain.ClientLink{}, fmt.Errorf("client link could not be built: %w", err)
	}
	link, err := domain.ParseClientLink(raw)
	if err != nil {
		return domain.ClientLink{}, fmt.Errorf("generated client link is invalid: %w", err)
	}
	return link, nil
}

func realityPayload(r Request, keys xui.KeyPair, shortID string) map[string]any {
	settings := encodeJSON(map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}})
	streamSettings := encodeJSON(map[string]any{"network": "tcp", "security": "reality", "externalProxy": []any{}, "realitySettings": map[string]any{"show": false, "xver": 0, "minClientVer": realityMinClientVer, "target": r.RealityTarget, "serverNames": []string{r.RealitySNI}, "privateKey": keys.PrivateKey, "shortIds": []string{shortID}, "settings": map[string]any{"publicKey": keys.PublicKey, "fingerprint": domain.DefaultFingerprint, "serverName": "", "spiderX": "/"}}, "tcpSettings": map[string]any{"acceptProxyProtocol": false, "header": map[string]any{"type": "none"}}})
	sniffing := encodeJSON(map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "metadataOnly": false, "routeOnly": false})
	return map[string]any{"enable": true, "remark": managedRemark, "listen": "", "port": r.ListenPort, "protocol": "vless", "expiryTime": 0, "total": 0, "settings": settings, "streamSettings": streamSettings, "sniffing": sniffing}
}

func encodeJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func systemdUnit() string {
	return `[Unit]
Description=3x-ui managed by vpnctl
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=vpnctl-xui
Group=vpnctl-xui
Environment="XRAY_VMESS_AEAD_FORCED=false"
WorkingDirectory=/usr/local/x-ui
ExecStart=/usr/local/x-ui/x-ui
Restart=on-failure
RestartSec=5s
UMask=0077
NoNewPrivileges=true
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
PrivateTmp=true
PrivateDevices=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/etc/x-ui /var/log/x-ui /usr/local/x-ui/bin
BindReadOnlyPaths=-/usr/local/x-ui/bin/xray-linux-amd64 -/usr/local/x-ui/bin/xray-linux-arm64
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictRealtime=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
`
}

func randomHex(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
func randomCredential(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, length)
	buf := make([]byte, length*2)
	n := 0
	for n < length {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, v := range buf {
			if int(v) >= 256-256%len(chars) {
				continue
			}
			out[n] = chars[int(v)%len(chars)]
			n++
			if n == length {
				break
			}
		}
	}
	return string(out), nil
}
func randomPrivilegedPort() (int, error) {
	for range 100 {
		raw := make([]byte, 2)
		if _, err := rand.Read(raw); err != nil {
			return 0, err
		}
		port := 512 + (int(raw[0])<<8|int(raw[1]))%512
		if listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, errors.New("no privileged loopback panel port is available")
}
func ensurePortAvailable(port int) error {
	listeners := []net.Listener{}
	for _, network := range []string{"tcp4", "tcp6"} {
		listener, err := net.Listen(network, fmt.Sprintf(":%d", port))
		if err != nil {
			for _, l := range listeners {
				l.Close()
			}
			return fmt.Errorf("port %d is already in use", port)
		}
		listeners = append(listeners, listener)
	}
	for _, l := range listeners {
		l.Close()
	}
	return nil
}
func probeReality(ctx context.Context, target, sni string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || port != "443" || sni == "" {
		return errors.New("Reality target must be host:443")
	}
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}}
	connection, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return fmt.Errorf("Reality TLS target validation failed: %w", err)
	}
	return connection.Close()
}

func waitAPIHealth(ctx context.Context, api *xui.Client) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := api.Status(ctx)
		if err == nil && status.XrayRunning() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Xray did not become healthy")
		case <-ticker.C:
		}
	}
}

func waitPanelAPI(ctx context.Context, api *xui.Client) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := api.Status(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("3x-ui API did not become ready")
		case <-ticker.C:
		}
	}
}

func freeDisk(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func requirePrivilegedPortBoundary(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("unprivileged port boundary could not be inspected")
	}
	boundary, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || boundary < 1024 {
		return errors.New("host permits unprivileged processes to impersonate the local panel")
	}
	return nil
}
func writeMarker(dir, id string) error {
	return writeAtomicNew(filepath.Join(dir, ".vpnctl-owned"), []byte(id+"\n"), 0600)
}

func createManagedDir(target, installID string) error {
	parent := filepath.Dir(target)
	stage := managedDirStage(target, installID)
	if err := os.Mkdir(stage, 0700); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = safeRemoveTreeWithin(parent, stage)
		}
	}()
	if err := writeMarker(stage, installID); err != nil {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		return err
	}
	keep = true
	return syncDirectory(parent)
}

func removeManagedDirStage(target, installID string) error {
	stage := managedDirStage(target, installID)
	if _, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := verifyMarker(stage, installID); err != nil {
		return err
	}
	return safeRemoveManagedTree(stage, installID)
}

func managedDirStage(target, installID string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".vpnctl-"+installID)
}
func verifyMarker(dir, id string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed directory is missing or unsafe")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".vpnctl-owned"))
	if err != nil || strings.TrimSpace(string(data)) != id {
		return errors.New("ownership marker mismatch")
	}
	return nil
}
func verifyMarkerOrMissing(dir, id string) (bool, error) {
	_, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := verifyMarker(dir, id); err != nil {
		return false, err
	}
	return true, nil
}

func serviceAccountExists() (bool, error) {
	_, err := user.Lookup(serviceUser)
	if err == nil {
		return true, nil
	}
	var unknown user.UnknownUserError
	if errors.As(err, &unknown) {
		return false, nil
	}
	return false, fmt.Errorf("lookup service account: %w", err)
}

func safeRemoveManagedTree(root, installID string) error {
	if err := verifyMarker(root, installID); err != nil {
		return err
	}
	return safeRemoveTreeWithin(filepath.Dir(root), root)
}

func safeRemoveTreeWithin(parent, target string) error {
	parent = filepath.Clean(parent)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(parent, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || strings.Contains(relative, string(os.PathSeparator)) {
		return errors.New("managed removal target is outside its expected parent")
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed removal parent is missing or unsafe")
	}
	targetInfo, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed removal target is missing or unsafe")
	}
	var parentStat, rootStat unix.Stat_t
	if err := unix.Lstat(parent, &parentStat); err != nil {
		return err
	}
	if err := unix.Lstat(target, &rootStat); err != nil {
		return err
	}
	if parentStat.Dev != rootStat.Dev {
		return errors.New("managed removal target is a mount point")
	}
	if err := filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed removal tree contains a symlink")
		}
		var stat unix.Stat_t
		if err := unix.Lstat(path, &stat); err != nil {
			return err
		}
		if stat.Dev != rootStat.Dev {
			return errors.New("managed removal tree contains a mount point")
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(target)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in managed config")
		}
		return os.Chown(path, uid, gid)
	})
}

func secureDataTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in managed data")
		}
		mode := os.FileMode(0600)
		if entry.IsDir() {
			mode = 0700
		}
		return os.Chmod(path, mode)
	})
}

func secureProgramTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in managed program")
		}
		mode := os.FileMode(0644)
		switch {
		case entry.IsDir():
			mode = 0755
		case entry.Name() == ".vpnctl-owned":
			mode = 0600
		case info.Mode().Perm()&0111 != 0:
			mode = 0755
		}
		return os.Chmod(path, mode)
	})
}

func writeAtomicNew(path string, data []byte, mode os.FileMode) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("refusing to overwrite %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAtomicReplace(path, data, mode)
}
func writeAtomicReplace(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vpnctl-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
