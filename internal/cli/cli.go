package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
	Service             *app.Service
	In                  io.Reader
	Out                 io.Writer
	Err                 io.Writer
	IsTTY               bool
	PublicAddressLookup func(context.Context) (string, error)
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

func (e silentExit) Error() string {
	return "команда завершилась с ненулевым кодом"
}

func (c *CLI) Run(ctx context.Context, args []string) int {
	opts, commandArgs, err := parseGlobals(args)
	if err != nil {
		return c.fail(Options{}, domain.E("INVALID_ARGUMENT", err.Error(), "Запусти vpnctl help", 2, err))
	}
	if opts.Output == "json" && opts.Color == "always" {
		return c.fail(opts, domain.E("INVALID_ARGUMENT", "--color always нельзя использовать с JSON", "Используй --color never", 2, nil))
	}
	if opts.Quiet && opts.Verbose {
		return c.fail(opts, domain.E("INVALID_ARGUMENT", "--quiet и --verbose нельзя использовать вместе", "Выбери один режим диагностики", 2, nil))
	}
	if opts.Interactive && opts.NonInteractive {
		return c.fail(opts, domain.E("INVALID_ARGUMENT", "--interactive и --non-interactive нельзя использовать вместе", "Выбери один режим", 2, nil))
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
		runErr = domain.E("UPDATE_UNAVAILABLE", "безопасное обновление пока недоступно", "Используй проверенный релиз после реализации отката", 3, nil)
	case "uninstall":
		runErr = c.uninstall(ctx, opts, commandArgs[1:])
	default:
		runErr = domain.E("UNKNOWN_COMMAND", fmt.Sprintf("неизвестная команда %q", commandArgs[0]), "Запусти vpnctl help", 2, nil)
	}
	if runErr != nil {
		return c.fail(opts, runErr)
	}
	return 0
}

func (c *CLI) install(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("install")
	user := set.String("user", "", "имя первого VPN-пользователя")
	serverName := set.String("server-name", "", "название сервера")
	publicAddress := set.String("public-address", "", "публичный IP-адрес")
	listenPort := set.Int("listen-port", 443, "порт VLESS")
	sshPort := set.Int("ssh-port", 0, "порт SSH")
	realitySNI := set.String("reality-server-name", "www.microsoft.com", "домен Reality")
	realityTarget := set.String("reality-destination", "www.microsoft.com:443", "назначение Reality")
	mode := set.String("mode", "recommended", "режим установки")
	panelAccess := set.String("panel-access", "local", "доступ к панели")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 0 {
		return usage("некорректные аргументы установки")
	}
	if *mode != "recommended" && *mode != "advanced" {
		return usage("режим должен быть recommended или advanced")
	}
	if *panelAccess != "local" {
		return domain.E("UNSAFE_PANEL_EXPOSURE", "поддерживается только локальная панель", "Используй --panel-access local", 3, nil)
	}
	interactive := opts.Interactive || c.IsTTY && !opts.NonInteractive
	if interactive {
		if err := c.installWizard(ctx, user, serverName, publicAddress, listenPort, sshPort, realitySNI, realityTarget, mode, opts.Yes); err != nil {
			return err
		}
	}
	if *user == "" {
		return usage("обязателен флаг --user")
	}
	if *publicAddress == "" {
		*publicAddress = c.detectPublicAddress(ctx)
	}
	if *publicAddress == "" {
		return domain.E("PUBLIC_ADDRESS_REQUIRED", "не удалось определить публичный IP-адрес", "Передай --public-address <IP>", 3, nil)
	}
	result, err := c.Service.Install(ctx, installer.Request{User: *user, ServerName: *serverName, PublicAddress: *publicAddress, ListenPort: *listenPort, SSHPort: *sshPort, RealitySNI: *realitySNI, RealityTarget: *realityTarget})
	if err != nil {
		return err
	}
	if opts.Output == "json" {
		return writeJSON(c.Out, map[string]any{"ok": true, "existing": result.Existing, "address": result.State.PublicAddress, "user": result.User.Name})
	}
	if result.Existing {
		fmt.Fprintln(c.Out, "Установка уже существует и работает. Изменения не требуются.")
		return nil
	}
	fmt.Fprintf(c.Out, "Установка завершена.\nАдрес: %s\nПротокол: VLESS TCP Reality\nПользователь: %s\nПоказать конфигурацию клиента: sudo vpnctl qr %s\n", result.State.PublicAddress, result.User.Name, result.User.Name)
	if interactive {
		return c.showUser(ctx, opts, result.User.Name, true)
	}
	return nil
}

func (c *CLI) installWizard(ctx context.Context, user, serverName, publicAddress *string, listenPort, sshPort *int, realitySNI, realityTarget, mode *string, confirmed bool) error {
	if c.In == nil {
		return domain.E("TTY_REQUIRED", "интерактивный ввод недоступен", "Используй --non-interactive и передай обязательные флаги", 3, nil)
	}
	renderer, err := ui.NewRenderer(c.Err, ui.Options{Terminal: ui.TerminalAlways, Color: ui.ColorMode("auto"), Unicode: ui.UnicodeMode("auto")})
	if err != nil {
		return err
	}
	defer renderer.Close()
	renderer.Banner("VPNCTL", "Безопасная настройка сервера")
	prompter := ui.NewPrompter(c.In, c.Err, true)
	selected, err := prompter.Select("Выбери режим установки", []ui.Choice{{Label: "Рекомендуемый", Description: "безопасные автоматические настройки"}, {Label: "Расширенный", Description: "ручные сетевые настройки"}}, 0)
	if err != nil {
		return domain.E("INSTALL_CANCELLED", "установка отменена", "Запусти vpnctl install ещё раз", 130, err)
	}
	if selected == 1 {
		*mode = "advanced"
	}
	hostname, _ := os.Hostname()
	if *serverName == "" {
		*serverName = hostname
	}
	if *publicAddress == "" {
		*publicAddress = c.detectPublicAddress(ctx)
	}
	validateName := func(value string) error { _, err := domain.ValidateUserName(value); return err }
	if *mode == "recommended" {
		*user, err = prompter.Input("Имя первого VPN-пользователя", "vpn", validateName)
		if err == nil && *publicAddress == "" {
			*publicAddress, err = prompter.Input("Публичный IP", "", validateIP)
		}
	} else {
		*serverName, err = prompter.Input("Название сервера", *serverName, requireText)
		if err == nil {
			*user, err = prompter.Input("Имя первого VPN-пользователя", "vpn", validateName)
		}
		if err == nil {
			*publicAddress, err = prompter.Input("Публичный IP", *publicAddress, validateIP)
		}
		if err == nil {
			value := strconv.Itoa(*listenPort)
			value, err = prompter.Input("Порт VLESS", value, validatePort)
			if err == nil {
				*listenPort, _ = strconv.Atoi(value)
			}
		}
		if err == nil {
			*realitySNI, err = prompter.Input("Домен Reality", *realitySNI, requireText)
		}
		if err == nil {
			*realityTarget, err = prompter.Input("Назначение Reality", *realityTarget, requireText)
		}
		if err == nil && *sshPort == 0 {
			*sshPort = sshPortFromConnection()
		}
	}
	if err != nil {
		return domain.E("INSTALL_CANCELLED", "установка отменена", "Запусти vpnctl install ещё раз", 130, err)
	}
	if *publicAddress == "" {
		return domain.E("PUBLIC_ADDRESS_REQUIRED", "не удалось определить публичный IP-адрес", "Выбери расширенный режим или передай --public-address <IP>", 3, nil)
	}
	renderer.Section("Всё готово к установке")
	renderer.Table([]ui.Column{{Title: "ПАРАМЕТР", MinWidth: 12}, {Title: "ЗНАЧЕНИЕ", MinWidth: 20}}, [][]string{{"Сервер", *serverName}, {"VPN-пользователь", *user}, {"Адрес", *publicAddress}, {"Протокол", "VLESS TCP Reality"}, {"Порт VPN", strconv.Itoa(*listenPort)}, {"Панель управления", "Только локально"}})
	if confirmed {
		return nil
	}
	ok, err := prompter.Confirm("Начать установку?", true)
	if err != nil || !ok {
		return domain.E("INSTALL_CANCELLED", "установка отменена", "Система не была изменена", 130, err)
	}
	return nil
}

func (c *CLI) detectPublicAddress(ctx context.Context) string {
	if address := publicAddressFromSSH(); address != "" {
		return address
	}
	lookup := c.PublicAddressLookup
	if lookup == nil {
		lookup = lookupPublicAddress
	}
	address, err := lookup(ctx)
	if err != nil {
		return ""
	}
	return address
}

func lookupPublicAddress(ctx context.Context) (string, error) {
	requestContext, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("public address endpoint redirected")
		},
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, "https://api64.ipify.org", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("public address endpoint returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65))
	if err != nil || len(data) > 64 {
		return "", errors.New("public address response is invalid")
	}
	address := net.ParseIP(strings.TrimSpace(string(data)))
	if address == nil || !address.IsGlobalUnicast() || address.IsPrivate() {
		return "", errors.New("public address response is not a public IP")
	}
	return address.String(), nil
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
		return errors.New("значение обязательно")
	}
	return nil
}

func validateIP(value string) error {
	if net.ParseIP(value) == nil {
		return errors.New("введи корректный IP-адрес")
	}
	return nil
}

func validatePort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("введи порт от 1 до 65535")
	}
	return nil
}

func (c *CLI) uninstall(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("uninstall")
	keepData := set.Bool("keep-data", false, "сохранить конфигурацию 3x-ui")
	removeBackups := set.Bool("remove-backups", false, "удалить резервные копии")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 0 {
		return usage("использование: vpnctl uninstall [--keep-data] --yes")
	}
	if !opts.Yes {
		return domain.E("CONFIRMATION_REQUIRED", "удаление требует подтверждения", "Повтори команду с --yes", 3, nil)
	}
	if err := c.Service.Uninstall(ctx, *keepData, *removeBackups); err != nil {
		return err
	}
	return c.result(opts, map[string]any{"ok": true, "uninstalled": true, "dataKept": *keepData, "binaryRetained": true}, "VPN-сервис удалён. Исполняемый файл vpnctl сохранён.\n")
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
		return usage("команда status не принимает аргументы")
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
	statusText := "РАБОТАЕТ"
	if status == "degraded" {
		statusText = "ЕСТЬ ПРОБЛЕМЫ"
	}
	fmt.Fprintf(c.Out, "Сервер\n------\nСтатус       %s\nАдрес        %s\nПротокол     VLESS TCP Reality\nПользователи %d\nXray         %s\n3x-ui        работает (только локально)\nВерсия       vpnctl %s\n", statusText, state.PublicAddress, len(users), server.Xray.State, domain.ProductVersion)
	if !server.XrayRunning() {
		return silentExit(5)
	}
	return nil
}

func (c *CLI) user(ctx context.Context, opts Options, args []string) error {
	if len(args) == 0 {
		return usage("нужна подкоманда user")
	}
	switch args[0] {
	case "add":
		set := newFlagSet("user add")
		show := set.Bool("show", false, "показать конфигурацию клиента")
		positionals, err := parseSet(set, args[1:])
		if err != nil || len(positionals) != 1 {
			return usage("использование: vpnctl user add <имя> [--show]")
		}
		if *show && opts.Output == "json" {
			return usage("--show нельзя использовать с JSON; используй user show")
		}
		user, created, err := c.Service.AddUser(ctx, positionals[0])
		if err != nil {
			return err
		}
		if opts.Output == "json" {
			return writeJSON(c.Out, map[string]any{"ok": true, "user": user, "created": created})
		}
		if !created {
			fmt.Fprintf(c.Out, "Пользователь %q уже существует. Изменений нет.\n", user.Name)
		} else {
			fmt.Fprintf(c.Out, "Пользователь %q создан.\n", user.Name)
		}
		if *show || (c.IsTTY && !opts.NonInteractive) {
			return c.showUser(ctx, opts, user.Name, true)
		}
		fmt.Fprintf(c.Out, "Показать конфигурацию клиента: sudo vpnctl qr %s\n", user.Name)
		return nil
	case "remove":
		if len(args) != 2 {
			return usage("использование: vpnctl user remove <имя> --yes")
		}
		if !opts.Yes {
			return domain.E("CONFIRMATION_REQUIRED", fmt.Sprintf("удаление VPN-пользователя %q требует подтверждения", args[1]), "Повтори команду с --yes", 3, nil)
		}
		remaining, err := c.Service.RemoveUser(ctx, args[1])
		if err != nil {
			return err
		}
		return c.result(opts, map[string]any{"ok": true, "removed": args[1], "remaining": remaining}, fmt.Sprintf("Пользователь %q удалён. Осталось пользователей: %d.\n", args[1], remaining))
	case "list":
		if len(args) != 1 {
			return usage("команда user list не принимает аргументы")
		}
		users, err := c.Service.ListUsers(ctx)
		if err != nil {
			return err
		}
		if opts.Output == "json" {
			return writeJSON(c.Out, map[string]any{"ok": true, "users": users})
		}
		if len(users) == 0 {
			fmt.Fprintln(c.Out, "VPN-пользователей нет. Добавить: sudo vpnctl user add <имя>")
			return nil
		}
		fmt.Fprintf(c.Out, "VPN-пользователи (%d)\n\nИМЯ                               СТАТУС\n", len(users))
		for _, user := range users {
			fmt.Fprintf(c.Out, "%-32s  %s\n", user.Name, enabledState(user.Enabled))
		}
		return nil
	case "show":
		if len(args) != 2 {
			return usage("использование: vpnctl user show <имя>")
		}
		return c.showUser(ctx, opts, args[1], c.IsTTY)
	default:
		return usage(fmt.Sprintf("неизвестная подкоманда user %q", args[0]))
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
	fmt.Fprintf(c.Out, "Клиент: %s\n\n%s\n\n%s\n", user.Name, terminalQR(link), link)
	return nil
}

func (c *CLI) qr(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("qr")
	format := set.String("format", "", "terminal или uri")
	compact := set.Bool("compact", false, "компактный QR-код в терминале")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 1 {
		return usage("использование: vpnctl qr <имя> [--format terminal|uri] [--compact]")
	}
	if *format == "" {
		if c.IsTTY {
			*format = "terminal"
		} else {
			*format = "uri"
		}
	}
	if *format != "terminal" && *format != "uri" {
		return usage("формат QR должен быть terminal или uri")
	}
	if *format == "terminal" && !c.IsTTY {
		return domain.E("TTY_REQUIRED", "для вывода QR нужен терминал", "Используй --format uri", 3, nil)
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
		return usage("автоматическое исправление doctor пока недоступно")
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
		fmt.Fprintln(c.Out, "Диагностика VPNCTL")
		group := ""
		for _, check := range checks {
			if check.Group != group {
				group = check.Group
				fmt.Fprintf(c.Out, "\n%s\n", group)
			}
			fmt.Fprintf(c.Out, "  %s %s\n", checkMark(check.Status), check.Name)
		}
		fmt.Fprintf(c.Out, "\nРезультат\n  успешно: %d, ошибок: %d\n", len(checks)-failed, failed)
	}
	if failed > 0 {
		return silentExit(5)
	}
	return nil
}

func (c *CLI) backup(ctx context.Context, opts Options, args []string) error {
	set := newFlagSet("backup")
	file := set.String("file", "", "путь к файлу")
	plaintext := set.Bool("plaintext", false, "подтвердить сохранение секретов без шифрования")
	positionals, err := parseSet(set, args)
	if err != nil || len(positionals) != 0 {
		return usage("использование: vpnctl backup [--file путь] --plaintext")
	}
	result, err := c.Service.Backup(ctx, *file, *plaintext)
	if err != nil {
		return err
	}
	return c.result(opts, map[string]any{"ok": true, "backup": result}, fmt.Sprintf("Резервная копия создана: %s\nРазмер: %d байт\nSHA-256: %s\nПредупреждение: %s\n", result.Path, result.Size, result.SHA256, result.Warning))
}

func (c *CLI) restore(_ context.Context, opts Options, args []string) error {
	if len(args) != 1 {
		return usage("использование: vpnctl restore <копия> --yes")
	}
	if !opts.Yes {
		return domain.E("CONFIRMATION_REQUIRED", "восстановление требует подтверждения", "Проверь копию и повтори команду с --yes", 3, nil)
	}
	return domain.E("RESTORE_UNAVAILABLE", "восстановление отключено: безопасный автономный откат ещё не реализован", "Сохрани копию и используй проверенную ручную процедуру восстановления", 3, nil)
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
	detail := errorDetail{Code: "OPERATION_FAILED", Message: "операция завершилась ошибкой"}
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
		fmt.Fprintf(c.Err, "ОШИБКА: %s\n", detail.Message)
		if detail.Hint != "" {
			fmt.Fprintf(c.Err, "Что делать: %s\n", detail.Hint)
		}
		fmt.Fprintf(c.Err, "Код ошибки: %s\n", detail.Code)
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
				return "", fmt.Errorf("для %s нужно значение", arg)
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
		return opts, nil, errors.New("output должен быть human или json")
	}
	if opts.Color != "auto" && opts.Color != "always" && opts.Color != "never" {
		return opts, nil, errors.New("color должен быть auto, always или never")
	}
	if opts.LogFormat != "text" && opts.LogFormat != "json" {
		return opts, nil, errors.New("log-format должен быть text или json")
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
					return nil, fmt.Errorf("для флага %s нужно значение", args[i])
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
		return "[QR недоступен]"
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
	return domain.E("INVALID_ARGUMENT", message, "Запусти vpnctl help", 2, nil)
}
func enabledState(value bool) string {
	if value {
		return "активен"
	}
	return "отключён"
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

const helpText = `vpnctl управляет VPN-сервером VLESS TCP Reality.

Использование:
  vpnctl install
  vpnctl status
  vpnctl user add|remove|list|show
  vpnctl qr <name>
  vpnctl doctor
  vpnctl backup --plaintext
  vpnctl restore <backup> --yes
  vpnctl update
  vpnctl uninstall

Общие флаги:
  --output human|json
  --color auto|always|never
  --log-format text|json
  --quiet --verbose --non-interactive --interactive --yes
`
