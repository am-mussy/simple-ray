package app

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/installer"
)

const (
	statusPassed      = "passed"
	statusFailed      = "failed"
	statusUnavailable = "unavailable"
)

// Probes are the read-only environment checks doctor runs. It is an interface
// so the diagnosis can be tested without a live server.
type Probes interface {
	ServiceActive(context.Context) installer.ProbeResult
	ClockSynchronized(context.Context) installer.ProbeResult
	PortListening(int) installer.ProbeResult
	FirewallPortOpen(context.Context, int) installer.ProbeResult
	InternetReachable(context.Context) installer.ProbeResult
	DNSWorking(context.Context) installer.ProbeResult
	RealityTargetHealthy(context.Context, string, string) installer.ProbeResult
	DiskSpace() installer.ProbeResult
	ConfigMatchesUsers(int, []string, string) installer.ProbeResult
	ClientCompatibility(int) installer.ProbeResult
}

// TunnelProbe pushes real traffic through a client link and reports the public
// address seen on the far side.
type TunnelProbe func(context.Context, string, domain.ClientLink) (string, error)

type checkList struct {
	checks []domain.Check
}

func (l *checkList) add(group, name, status, reason, hint string) {
	l.checks = append(l.checks, domain.Check{Group: group, Name: name, Status: status, Reason: reason, Hint: hint})
}

// fromProbe records a probe result, mapping a probe that could not run onto a
// warning rather than a failure of the user's VPN.
func (l *checkList) fromProbe(group, name string, result installer.ProbeResult, hint string) bool {
	switch {
	case result.Skipped:
		l.add(group, name, statusUnavailable, result.Detail, "")
		return true
	case result.OK:
		l.add(group, name, statusPassed, result.Detail, "")
		return true
	default:
		l.add(group, name, statusFailed, result.Detail, hint)
		return false
	}
}

// Doctor diagnoses the installation end to end. The final tunnel check pushes
// real traffic through the exact link a user is given, because every other
// check can pass while Reality quietly relays clients to its decoy site.
func (s *Service) Doctor(ctx context.Context, quick bool) []domain.Check {
	list := &checkList{}
	installation, api, err := s.api()
	if err != nil {
		list.add("Система", "состояние vpnctl", statusFailed, err.Error(), hintOf(err))
		return list.checks
	}
	if err := domain.ValidateState(installation); err != nil {
		list.add("Система", "состояние vpnctl", statusFailed, err.Error(),
			"Переустанови сервер: sudo vpnctl install")
		return list.checks
	}
	list.add("Система", "состояние vpnctl", statusPassed, "", "")

	probes := s.probes()
	list.fromProbe("Система", "служба 3x-ui", probes.ServiceActive(ctx),
		"Запусти службу: sudo systemctl start x-ui")
	list.fromProbe("Система", "часы сервера", probes.ClockSynchronized(ctx),
		"Включи синхронизацию времени: sudo timedatectl set-ntp true. Reality отвергает клиентов, если время сервера ушло")
	list.fromProbe("Система", "свободное место", probes.DiskSpace(),
		"Освободи место на диске сервера")

	status, statusErr := api.Status(ctx)
	if statusErr != nil {
		list.add("Сервисы", "API 3x-ui", statusFailed, "локальный API недоступен",
			"Перезапусти панель: sudo systemctl restart x-ui")
	} else {
		list.add("Сервисы", "API 3x-ui", statusPassed, "", "")
		if status.XrayRunning() {
			list.add("Сервисы", "ядро Xray", statusPassed, "", "")
		} else {
			list.add("Сервисы", "ядро Xray", statusFailed, "ядро Xray не запущено",
				"Перезапусти: sudo systemctl restart x-ui")
		}
	}

	if installation.PanelListen == "127.0.0.1" {
		list.add("Сеть", "панель доступна только локально", statusPassed, "", "")
	} else {
		list.add("Сеть", "панель доступна только локально", statusFailed,
			"панель слушает "+installation.PanelListen,
			"Панель должна быть только на 127.0.0.1. Переустанови сервер")
	}
	list.fromProbe("Сеть", "порт VPN слушается", probes.PortListening(installation.ListenPort),
		"Перезапусти: sudo systemctl restart x-ui")
	list.fromProbe("Сеть", "порт VPN открыт в firewall", probes.FirewallPortOpen(ctx, installation.ListenPort),
		fmt.Sprintf("Открой порт: sudo ufw allow %d/tcp", installation.ListenPort))
	list.fromProbe("Сеть", "исходящая связь", probes.InternetReachable(ctx),
		"Сервер не выходит в интернет. Проверь сеть у хостинг-провайдера")
	list.fromProbe("Сеть", "DNS", probes.DNSWorking(ctx),
		"Сервер не резолвит домены. Проверь настройки DNS в /etc/resolv.conf")
	list.fromProbe("Сеть", "сайт-прикрытие Reality",
		probes.RealityTargetHealthy(ctx, installation.RealityTarget, installation.RealitySNI),
		"Сайт-прикрытие недоступен или не поддерживает TLS 1.3. Переустанови сервер с другим сайтом")

	list.fromProbe("Конфигурация", "совместимость клиентов", probes.ClientCompatibility(installation.ListenPort),
		"Xray отвергает старые клиенты и молча отдаёт их сайту-прикрытию: подключение есть, трафика нет. "+
			"Задай minClientVer=0.0.0 в настройках Reality или переустанови сервер")

	users, usersErr := api.ListClients(ctx)
	if usersErr != nil {
		list.add("Конфигурация", "VPN-пользователи", statusFailed, "не удалось прочитать список пользователей",
			"Перезапусти: sudo systemctl restart x-ui")
		return list.checks
	}
	managed := toUsersForInbound(users, installation.InboundID)
	if len(managed) == 0 {
		list.add("Конфигурация", "VPN-пользователи", statusFailed, "нет ни одного VPN-пользователя",
			"Создай пользователя: sudo vpnctl user add <имя>")
		return list.checks
	}
	list.add("Конфигурация", "VPN-пользователи", statusPassed, fmt.Sprintf("пользователей: %d", len(managed)), "")

	names := make([]string, 0, len(managed))
	for _, user := range managed {
		names = append(names, user.Name)
	}
	drift := probes.ConfigMatchesUsers(installation.ListenPort, names, installation.RealitySNI)
	switch {
	case drift.Skipped:
		list.add("Конфигурация", "конфигурация Xray синхронна", statusUnavailable, drift.Detail, "")
	case drift.OK:
		list.add("Конфигурация", "конфигурация Xray синхронна", statusPassed, drift.Detail, "")
	default:
		// 3x-ui applies user changes to the live process over its API without
		// rewriting the file, so this lags legitimately and must not read as a
		// broken VPN. It matters because the stale file misleads diagnosis.
		list.add("Конфигурация", "конфигурация Xray синхронна", statusUnavailable, drift.Detail,
			"Файл конфигурации отстал от базы 3x-ui. Обычно это безвредно; чтобы синхронизировать: sudo systemctl restart x-ui")
	}

	subject := managed[0].Name
	link, linkErr := s.clientLink(ctx, api, installation, subject)
	if linkErr != nil {
		list.add("Конфигурация", "ссылка для клиента", statusFailed, linkErr.Error(),
			fmt.Sprintf("Пересоздай пользователя: sudo vpnctl user remove %s && sudo vpnctl user add %s", subject, subject))
		return list.checks
	}
	list.add("Конфигурация", "ссылка для клиента", statusPassed, "проверена для "+subject, "")

	if quick {
		list.add("Туннель", "сквозная проверка трафика", statusUnavailable,
			"пропущена по --quick", "Полная проверка: sudo vpnctl doctor")
		return list.checks
	}
	s.checkTunnel(ctx, list, installation, link, subject)
	return list.checks
}

// checkTunnel is the decisive check: it proves the link a user is given both
// authenticates and carries traffic out through this server.
func (s *Service) checkTunnel(ctx context.Context, list *checkList, installation domain.State, link domain.ClientLink, subject string) {
	binary := installer.XrayBinary(installation.Architecture)
	observed, err := s.tunnelProbe()(ctx, binary, link)
	if err != nil {
		list.add("Туннель", "сквозная проверка трафика", statusFailed, err.Error(),
			fmt.Sprintf("Сервер не проксирует трафик по выданной ссылке. Удали профиль в приложении и импортируй заново: sudo vpnctl qr %s. Если не помогло: sudo systemctl restart x-ui", subject))
		return
	}
	if observed != installation.PublicAddress {
		list.add("Туннель", "сквозная проверка трафика", statusUnavailable,
			fmt.Sprintf("трафик идёт, но внешний адрес %s отличается от адреса сервера %s", observed, installation.PublicAddress),
			"Это нормально, если провайдер использует NAT или дополнительный исходящий адрес")
		return
	}
	// The probe reaches the server's own public address, which the kernel routes
	// over loopback, so it proves the server side and nothing about the path a
	// phone takes. A client app whose uTLS build cannot produce this
	// fingerprint still lands on the decoy site, so name the lever here rather
	// than let a green check end the diagnosis.
	list.add("Туннель", "сквозная проверка трафика", statusPassed,
		"трафик идёт, внешний адрес "+observed,
		fmt.Sprintf("Проверено со стороны сервера, fingerprint %s. Если на телефоне подключение есть, а трафика нет, выдай ссылку с другим профилем: sudo vpnctl qr %s --fingerprint chrome", link.Fingerprint, subject))
}

// clientLink returns the exact link a user would be handed, parsed and checked
// against the managed endpoint.
func (s *Service) clientLink(ctx context.Context, api API, installation domain.State, name string) (domain.ClientLink, error) {
	links, err := api.ClientLinks(ctx, name)
	if err != nil {
		return domain.ClientLink{}, fmt.Errorf("не удалось получить ссылку: %w", err)
	}
	if len(links) == 0 {
		return domain.ClientLink{}, fmt.Errorf("3x-ui не вернул ссылку для %s", name)
	}
	raw, err := domain.PublicClientLink(links[0], installation.PublicAddress, installation.ListenPort, "")
	if err != nil {
		return domain.ClientLink{}, fmt.Errorf("3x-ui вернул некорректную ссылку: %w", err)
	}
	link, err := domain.ParseClientLink(raw)
	if err != nil {
		return domain.ClientLink{}, err
	}
	if err := link.MatchesState(installation); err != nil {
		return domain.ClientLink{}, err
	}
	if net.ParseIP(link.Address) == nil {
		return domain.ClientLink{}, fmt.Errorf("адрес сервера в ссылке не является IP")
	}
	return link, nil
}

func (s *Service) probes() Probes {
	if s.Diagnostics != nil {
		return s.Diagnostics
	}
	return installer.NewDiagnostics()
}

func (s *Service) tunnelProbe() TunnelProbe {
	if s.Tunnel != nil {
		return s.Tunnel
	}
	return installer.ProbeClientLink
}

func hintOf(err error) string {
	var domainError *domain.Error
	if errors.As(err, &domainError) && domainError.Hint != "" {
		return domainError.Hint
	}
	return "Запусти установку: sudo vpnctl install"
}
