package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/mussy/simple-ray/internal/app"
	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/installer"
	"github.com/mussy/simple-ray/internal/ui"
)

type Options struct {
	Output         string
	Color          string
	LogFormat      string
	Quiet          bool
	Verbose        bool
	NonInteractive bool
	Interactive    bool
	Yes            bool
}

type CLI struct {
	Service *app.Service
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	IsTTY   bool
}

type errorResult struct {
	OK    bool        `json:"ok"`
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type silentExit int

func (e silentExit) Error() string { return "command completed with a non-zero result" }

func (c *CLI) Run(ctx context.Context, args []string) int {
	opts, commandArgs, err := parseGlobals(args)
	if err != nil {
		return c.fail(Options{}, domain.E("INVALID_ARGUMENT", err.Error(), "Run vpnctl help", 2, err))
	}
	if opts.Output == "json" && opts.Color == "always" {
		return c.fail(opts, domain.E("INVALID_ARGUMENT", "--color always cannot be used with JSON output", "Use --color never", 2, nil))
	}
	if opts.Quiet && opts.Verbose {
		return c.fail(opts, domain.E("INVALID_ARGUMENT", "--quiet and --verbose are mutually exclusive", "Choose one diagnostic mode", 2, nil))
	}
	if opts.Interactive && opts.NonInteractive {
		return c.fail(opts, domain.E("INVALID_ARGUMENT", "--interactive and --non-interactive are mutually exclusive", "Choose one mode", 2, nil))
	}
	if len(commandArgs) == 0 || commandArgs[0] == "help" || commandArgs[0] == "--help" {
		fmt.Fprint(c.Out, helpText)
		return 0
	}
	var runErr error
	switch commandArgs[0] {
	case "version":
		runErr = c.version(opts)
	case "status":
		runErr = c.status(ctx, opts, commandArgs[1:])
	case "user":
		runErr = c.user(ctx, opts, commandArgs[1:])
	case "qr":
		runErr = c.qr(ctx, opts, commandArgs[1:])
	case "doctor":
		runErr = c.doctor(ctx, opts, commandArgs[1:])
	case "backup":
		runErr = c.backup(ctx, opts, commandArgs[1:])
	case "restore":
		runErr = c.restore(ctx, opts, commandArgs[1:])
	case "install":
		runErr = c.install(ctx, opts, commandArgs[1:])
	case "update":
		runErr = domain.E("UPDATE_UNAVAILABLE", "safe updates are not enabled in this development build", "Use a verified release after rollback validation", 3, nil)
	case "uninstall":
		runErr = c.uninstall(ctx, opts, commandArgs[1:])
	default:
		runErr = domain.E("UNKNOWN_COMMAND", fmt.Sprintf("unknown command %q", commandArgs[0]), "Run vpnctl help", 2, nil)
	}
	if runErr != nil {
		return c.fail(opts, runErr)
	}
	return 0
}

func (c *CLI) install(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("install")
	user := set.String("user", "", "first VPN user")
	serverName := set.String("server-name", "", "server display name")
	publicAddress := set.String("public-address", "", "public IP address")
	listenPort := set.Int("listen-port", 443, "VLESS port")
	sshPort := set.Int("ssh-port", 0, "SSH listener port")
	realitySNI := set.String("reality-server-name", "www.microsoft.com", "Reality server name")
	realityTarget := set.String("reality-destination", "www.microsoft.com:443", "Reality target")
	mode := set.String("mode", "recommended", "setup mode")
	panelAccess := set.String("panel-access", "local", "panel access")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 0 {
		return usage("invalid install arguments")
	}
	if *mode != "recommended" && *mode != "advanced" {
		return usage("mode must be recommended or advanced")
	}
	if *panelAccess != "local" {
		return domain.E("UNSAFE_PANEL_EXPOSURE", "only a loopback panel is supported", "Use --panel-access local", 3, nil)
	}
	interactive := opts.Interactive || c.IsTTY && !opts.NonInteractive
	if interactive {
		if err := c.installWizard(user, serverName, publicAddress, listenPort, sshPort, realitySNI, realityTarget, mode, opts.Yes); err != nil {
			return err
		}
	}
	if *user == "" {
		return usage("--user is required")
	}
	if *publicAddress == "" {
		*publicAddress = publicAddressFromSSH()
	}
	if *publicAddress == "" {
		return domain.E("PUBLIC_ADDRESS_REQUIRED", "public address could not be detected from SSH", "Pass --public-address <IP>", 3, nil)
	}
	result, err := c.Service.Install(ctx, installer.Request{User: *user, ServerName: *serverName, PublicAddress: *publicAddress, ListenPort: *listenPort, SSHPort: *sshPort, RealitySNI: *realitySNI, RealityTarget: *realityTarget})
	if err != nil {
		return err
	}
	if opts.Output == "json" {
		return writeJSON(c.Out, map[string]any{"ok": true, "existing": result.Existing, "address": result.State.PublicAddress, "user": result.User.Name})
	}
	if result.Existing {
		fmt.Fprintln(c.Out, "Existing installation is healthy. Nothing to change.")
		return nil
	}
	fmt.Fprintf(c.Out, "Installation complete.\nAddress: %s\nProtocol: VLESS TCP Reality\nUser: %s\nShow the client configuration with: sudo vpnctl qr %s\n", result.State.PublicAddress, result.User.Name, result.User.Name)
	if interactive {
		return c.showUser(ctx, opts, result.User.Name, true)
	}
	return nil
}

func (c *CLI) installWizard(user, serverName, publicAddress *string, listenPort, sshPort *int, realitySNI, realityTarget, mode *string, confirmed bool) error {
	if c.In == nil {
		return domain.E("TTY_REQUIRED", "interactive input is unavailable", "Use --non-interactive with required flags", 3, nil)
	}
	renderer, err := ui.NewRenderer(c.Err, ui.Options{Terminal: ui.TerminalAlways, Color: ui.ColorMode("auto"), Unicode: ui.UnicodeMode("auto")})
	if err != nil {
		return err
	}
	defer renderer.Close()
	renderer.Banner("VPNCTL", "Secure server setup")
	prompter := ui.NewPrompter(c.In, c.Err, true)
	selected, err := prompter.Select("Choose setup", []ui.Choice{{Label: "Recommended", Description: "automatic secure defaults"}, {Label: "Advanced", Description: "network settings"}}, 0)
	if err != nil {
		return domain.E("INSTALL_CANCELLED", "installation was cancelled", "Run vpnctl install again", 130, err)
	}
	if selected == 1 {
		*mode = "advanced"
	}
	hostname, _ := os.Hostname()
	if *serverName == "" {
		*serverName = hostname
	}
	if *publicAddress == "" {
		*publicAddress = publicAddressFromSSH()
	}
	validateName := func(value string) error { _, err := domain.ValidateUserName(value); return err }
	if *mode == "recommended" {
		*user, err = prompter.Input("Create your first VPN user", "vpn", validateName)
	} else {
		*serverName, err = prompter.Input("Server name", *serverName, requireText)
		if err == nil {
			*user, err = prompter.Input("Create your first VPN user", "vpn", validateName)
		}
		if err == nil {
			*publicAddress, err = prompter.Input("Public IP", *publicAddress, validateIP)
		}
		if err == nil {
			value := strconv.Itoa(*listenPort)
			value, err = prompter.Input("VLESS port", value, validatePort)
			if err == nil {
				*listenPort, _ = strconv.Atoi(value)
			}
		}
		if err == nil {
			*realitySNI, err = prompter.Input("Reality server name", *realitySNI, requireText)
		}
		if err == nil {
			*realityTarget, err = prompter.Input("Reality destination", *realityTarget, requireText)
		}
		if err == nil && *sshPort == 0 {
			*sshPort = sshPortFromConnection()
		}
	}
	if err != nil {
		return domain.E("INSTALL_CANCELLED", "installation was cancelled", "Run vpnctl install again", 130, err)
	}
	if *publicAddress == "" {
		return domain.E("PUBLIC_ADDRESS_REQUIRED", "public address could not be detected from SSH", "Use Advanced or pass --public-address <IP>", 3, nil)
	}
	renderer.Section("Ready to install")
	renderer.Table([]ui.Column{{Title: "SETTING", MinWidth: 12}, {Title: "VALUE", MinWidth: 20}}, [][]string{{"Server", *serverName}, {"VPN user", *user}, {"Address", *publicAddress}, {"Protocol", "VLESS TCP Reality"}, {"VPN port", strconv.Itoa(*listenPort)}, {"Admin panel", "Local only"}})
	if confirmed {
		return nil
	}
	ok, err := prompter.Confirm("Install now?", true)
	if err != nil || !ok {
		return domain.E("INSTALL_CANCELLED", "installation was cancelled", "No system changes were made", 130, err)
	}
	return nil
}

func publicAddressFromSSH() string {
	fields := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(fields) != 4 {
		return ""
	}
	address := net.ParseIP(fields[2])
	if address == nil || address.IsPrivate() || address.IsLoopback() || address.IsUnspecified() {
		return ""
	}
	return address.String()
}

func sshPortFromConnection() int {
	fields := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(fields) != 4 {
		return 0
	}
	port, _ := strconv.Atoi(fields[3])
	return port
}

func requireText(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value is required")
	}
	return nil
}

func validateIP(value string) error {
	if net.ParseIP(value) == nil {
		return errors.New("enter a valid IP address")
	}
	return nil
}

func validatePort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("enter a port from 1 to 65535")
	}
	return nil
}

func (c *CLI) uninstall(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("uninstall")
	keepData := set.Bool("keep-data", false, "keep 3x-ui configuration")
	removeBackups := set.Bool("remove-backups", false, "remove backups")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 0 {
		return usage("usage: vpnctl uninstall [--keep-data] --yes")
	}
	if !opts.Yes {
		return domain.E("CONFIRMATION_REQUIRED", "uninstall requires confirmation", "Retry with --yes", 3, nil)
	}
	if err := c.Service.Uninstall(ctx, *keepData, *removeBackups); err != nil {
		return err
	}
	return c.result(opts, map[string]any{"ok": true, "uninstalled": true, "dataKept": *keepData, "binaryRetained": true}, "The managed VPN service was uninstalled. The vpnctl binary was retained.\n")
}

func (c *CLI) version(opts Options) error {
	if opts.Output == "json" {
		return writeJSON(c.Out, map[string]any{"ok": true, "version": domain.ProductVersion})
	}
	fmt.Fprintf(c.Out, "vpnctl %s\n", domain.ProductVersion)
	return nil
}

func (c *CLI) status(ctx context.Context, opts Options, args []string) error {
	if len(args) != 0 {
		return usage("status accepts no arguments")
	}
	state, server, users, err := c.Service.Status(ctx)
	if err != nil {
		return err
	}
	status := "online"
	if !server.XrayRunning() {
		status = "degraded"
	}
	if opts.Output == "json" {
		if err := writeJSON(c.Out, map[string]any{"ok": server.XrayRunning(), "status": status, "address": state.PublicAddress, "protocol": "VLESS TCP Reality", "users": len(users), "xray": map[string]any{"state": server.Xray.State, "version": server.Xray.Version}, "panel": map[string]any{"state": "running", "exposure": "local"}, "version": domain.ProductVersion}); err != nil {
			return err
		}
		if !server.XrayRunning() {
			return silentExit(5)
		}
		return nil
	}
	fmt.Fprintf(c.Out, "Server\n------\nStatus       %s\nAddress      %s\nProtocol     VLESS TCP Reality\nUsers        %d\nXray         %s\n3x-ui        running (local only)\nVersion      vpnctl %s\n", strings.ToUpper(status), state.PublicAddress, len(users), server.Xray.State, domain.ProductVersion)
	if !server.XrayRunning() {
		return silentExit(5)
	}
	return nil
}

func (c *CLI) user(ctx context.Context, opts Options, args []string) error {
	if len(args) == 0 {
		return usage("user subcommand is required")
	}
	switch args[0] {
	case "add":
		set := newFlagSet("user add")
		show := set.Bool("show", false, "show client configuration")
		positionals, err := parseSet(set, args[1:])
		if err != nil || len(positionals) != 1 {
			return usage("usage: vpnctl user add <name> [--show]")
		}
		if *show && opts.Output == "json" {
			return usage("--show cannot be used with JSON; use user show")
		}
		user, created, err := c.Service.AddUser(ctx, positionals[0])
		if err != nil {
			return err
		}
		if opts.Output == "json" {
			return writeJSON(c.Out, map[string]any{"ok": true, "user": user, "created": created})
		}
		if !created {
			fmt.Fprintf(c.Out, "User %q already exists. No changes made.\n", user.Name)
		} else {
			fmt.Fprintf(c.Out, "User %q created.\n", user.Name)
		}
		if *show || (c.IsTTY && !opts.NonInteractive) {
			return c.showUser(ctx, opts, user.Name, true)
		}
		fmt.Fprintf(c.Out, "Show the client configuration with: sudo vpnctl qr %s\n", user.Name)
		return nil
	case "remove":
		if len(args) != 2 {
			return usage("usage: vpnctl user remove <name> --yes")
		}
		if !opts.Yes {
			return domain.E("CONFIRMATION_REQUIRED", fmt.Sprintf("removing VPN user %q requires confirmation", args[1]), "Retry with --yes", 3, nil)
		}
		remaining, err := c.Service.RemoveUser(ctx, args[1])
		if err != nil {
			return err
		}
		return c.result(opts, map[string]any{"ok": true, "removed": args[1], "remaining": remaining}, fmt.Sprintf("User %q removed. %d users remain.\n", args[1], remaining))
	case "list":
		if len(args) != 1 {
			return usage("user list accepts no arguments")
		}
		users, err := c.Service.ListUsers(ctx)
		if err != nil {
			return err
		}
		if opts.Output == "json" {
			return writeJSON(c.Out, map[string]any{"ok": true, "users": users})
		}
		if len(users) == 0 {
			fmt.Fprintln(c.Out, "No VPN users. Add one with: sudo vpnctl user add <name>")
			return nil
		}
		fmt.Fprintf(c.Out, "VPN users (%d)\n\nNAME                              STATUS\n", len(users))
		for _, user := range users {
			fmt.Fprintf(c.Out, "%-32s  %s\n", user.Name, enabledState(user.Enabled))
		}
		return nil
	case "show":
		if len(args) != 2 {
			return usage("usage: vpnctl user show <name>")
		}
		return c.showUser(ctx, opts, args[1], c.IsTTY)
	default:
		return usage(fmt.Sprintf("unknown user subcommand %q", args[0]))
	}
}

func (c *CLI) showUser(ctx context.Context, opts Options, name string, decorated bool) error {
	user, link, err := c.Service.UserLink(ctx, name)
	if err != nil {
		return err
	}
	if opts.Output == "json" {
		return writeJSON(c.Out, map[string]any{"ok": true, "sensitive": true, "user": user, "uri": link})
	}
	if !decorated {
		fmt.Fprintln(c.Out, link)
		return nil
	}
	fmt.Fprintf(c.Out, "Client: %s\n\n%s\n\n%s\n", user.Name, terminalQR(link), link)
	return nil
}

func (c *CLI) qr(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("qr")
	format := set.String("format", "", "terminal or uri")
	compact := set.Bool("compact", false, "compact terminal QR")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 1 {
		return usage("usage: vpnctl qr <name> [--format terminal|uri] [--compact]")
	}
	if *format == "" {
		if c.IsTTY {
			*format = "terminal"
		} else {
			*format = "uri"
		}
	}
	if *format != "terminal" && *format != "uri" {
		return usage("QR format must be terminal or uri")
	}
	if *format == "terminal" && !c.IsTTY {
		return domain.E("TTY_REQUIRED", "terminal QR output requires a terminal", "Use --format uri", 3, nil)
	}
	_, link, err := c.Service.UserLink(ctx, positionals[0])
	if err != nil {
		return err
	}
	if *format == "uri" {
		fmt.Fprintln(c.Out, link)
	} else if *compact {
		fmt.Fprintln(c.Out, terminalQRCompact(link))
	} else {
		fmt.Fprintln(c.Out, terminalQR(link))
	}
	return nil
}

func (c *CLI) doctor(ctx context.Context, opts Options, args []string) error {
	if len(args) != 0 {
		return usage("doctor repair is not available in this build")
	}
	checks := c.Service.Doctor(ctx)
	failed := 0
	for _, check := range checks {
		if check.Status == "failed" {
			failed++
		}
	}
	if opts.Output == "json" {
		if err := writeJSON(c.Out, map[string]any{"ok": failed == 0, "checks": checks, "passed": len(checks) - failed, "failed": failed}); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(c.Out, "VPNCTL Doctor")
		group := ""
		for _, check := range checks {
			if check.Group != group {
				group = check.Group
				fmt.Fprintf(c.Out, "\n%s\n", group)
			}
			fmt.Fprintf(c.Out, "  %s %s\n", checkMark(check.Status), check.Name)
		}
		fmt.Fprintf(c.Out, "\nResult\n  %d passed, %d failed\n", len(checks)-failed, failed)
	}
	if failed > 0 {
		return silentExit(5)
	}
	return nil
}

func (c *CLI) backup(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("backup")
	file := set.String("file", "", "destination")
	plaintext := set.Bool("plaintext", false, "acknowledge unencrypted secrets")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 0 {
		return usage("usage: vpnctl backup [--file path] --plaintext")
	}
	result, err := c.Service.Backup(ctx, *file, *plaintext)
	if err != nil {
		return err
	}
	return c.result(opts, map[string]any{"ok": true, "backup": result}, fmt.Sprintf("Backup created: %s\nSize: %d bytes\nSHA-256: %s\nWarning: %s\n", result.Path, result.Size, result.SHA256, result.Warning))
}

func (c *CLI) restore(_ context.Context, opts Options, args []string) error {
	if len(args) != 1 {
		return usage("usage: vpnctl restore <backup> --yes")
	}
	if !opts.Yes {
		return domain.E("CONFIRMATION_REQUIRED", "restore requires confirmation", "Retry with --yes after reviewing the backup", 3, nil)
	}
	return domain.E("RESTORE_UNAVAILABLE", "restore is disabled because an offline rollback transaction is not implemented", "Keep the backup and restore it manually with a reviewed recovery procedure", 3, nil)
}

func (c *CLI) result(opts Options, data any, human string) error {
	if opts.Output == "json" {
		return writeJSON(c.Out, data)
	}
	fmt.Fprint(c.Out, human)
	return nil
}

func (c *CLI) fail(opts Options, err error) int {
	var silent silentExit
	if errors.As(err, &silent) {
		return int(silent)
	}
	detail := errorDetail{Code: "OPERATION_FAILED", Message: "operation failed"}
	exit := 1
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		detail.Code, detail.Message, detail.Hint = domainError.Code, domainError.Message, domainError.Hint
		exit = domainError.ExitCode
	} else if err != nil {
		detail.Message = err.Error()
	}
	if opts.Output == "json" {
		_ = writeJSON(c.Out, errorResult{OK: false, Error: detail})
	} else {
		fmt.Fprintf(c.Err, "ERROR: %s\n", detail.Message)
		if detail.Hint != "" {
			fmt.Fprintf(c.Err, "Suggested action: %s\n", detail.Hint)
		}
		fmt.Fprintf(c.Err, "Error code: %s\n", detail.Code)
	}
	return exit
}

func parseGlobals(args []string) (Options, []string, error) {
	var opts Options
	opts.Output, opts.Color, opts.LogFormat = "human", "auto", "text"
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		take := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "--output", "--color", "--log-format":
			value, err := take()
			if err != nil {
				return opts, nil, err
			}
			if arg == "--output" {
				opts.Output = value
			} else if arg == "--color" {
				opts.Color = value
			} else {
				opts.LogFormat = value
			}
		case "--quiet":
			opts.Quiet = true
		case "--verbose":
			opts.Verbose = true
		case "--non-interactive":
			opts.NonInteractive = true
		case "--interactive":
			opts.Interactive = true
		case "--yes":
			opts.Yes = true
		default:
			rest = append(rest, arg)
		}
	}
	if opts.Output != "human" && opts.Output != "json" {
		return opts, nil, errors.New("output must be human or json")
	}
	if opts.Color != "auto" && opts.Color != "always" && opts.Color != "never" {
		return opts, nil, errors.New("color must be auto, always, or never")
	}
	if opts.LogFormat != "text" && opts.LogFormat != "json" {
		return opts, nil, errors.New("log format must be text or json")
	}
	return opts, rest, nil
}

func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

func parseSet(set *flag.FlagSet, args []string) ([]string, error) {
	var flags, positions []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			flags = append(flags, args[i])
			name := strings.TrimPrefix(args[i], "--")
			if f := set.Lookup(name); f != nil && f.DefValue != "false" && !strings.Contains(args[i], "=") {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("flag %s requires a value", args[i])
				}
				i++
				flags = append(flags, args[i])
			}
		} else {
			positions = append(positions, args[i])
		}
	}
	if err := set.Parse(flags); err != nil {
		return nil, err
	}
	return positions, nil
}

func terminalQR(value string) string        { return renderQR(value, false) }
func terminalQRCompact(value string) string { return renderQR(value, true) }

func renderQR(value string, compact bool) string {
	code, err := qrcode.New(value, qrcode.Medium)
	if err != nil {
		return "[QR unavailable]"
	}
	bitmap := code.Bitmap()
	border := 2
	var result strings.Builder
	if !compact {
		for y := -border; y < len(bitmap)+border; y++ {
			for x := -border; x < len(bitmap)+border; x++ {
				if y >= 0 && y < len(bitmap) && x >= 0 && x < len(bitmap) && bitmap[y][x] {
					result.WriteString("██")
				} else {
					result.WriteString("  ")
				}
			}
			result.WriteByte('\n')
		}
		return strings.TrimSuffix(result.String(), "\n")
	}
	for y := -border; y < len(bitmap)+border; y += 2 {
		for x := -border; x < len(bitmap)+border; x++ {
			top := y >= 0 && y < len(bitmap) && x >= 0 && x < len(bitmap) && bitmap[y][x]
			bottom := y+1 >= 0 && y+1 < len(bitmap) && x >= 0 && x < len(bitmap) && bitmap[y+1][x]
			switch {
			case top && bottom:
				result.WriteRune('█')
			case top:
				result.WriteRune('▀')
			case bottom:
				result.WriteRune('▄')
			default:
				result.WriteByte(' ')
			}
		}
		result.WriteByte('\n')
	}
	return strings.TrimSuffix(result.String(), "\n")
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
func usage(message string) error {
	return domain.E("INVALID_ARGUMENT", message, "Run vpnctl help", 2, nil)
}
func enabledState(value bool) string {
	if value {
		return "active"
	}
	return "disabled"
}
func checkMark(status string) string {
	if status == "passed" {
		return "OK"
	}
	if status == "unavailable" {
		return "WARN"
	}
	return "ERROR"
}

const helpText = `vpnctl manages a VLESS TCP Reality server.

Usage:
  vpnctl install
  vpnctl status
  vpnctl user add|remove|list|show
  vpnctl qr <name>
  vpnctl doctor
  vpnctl backup --plaintext
  vpnctl restore <backup> --yes
  vpnctl update
  vpnctl uninstall

Global flags:
  --output human|json
  --color auto|always|never
  --log-format text|json
  --quiet --verbose --non-interactive --interactive --yes
`
