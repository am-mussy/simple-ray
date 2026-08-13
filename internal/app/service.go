package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mussy/simple-ray/internal/backup"
	"github.com/mussy/simple-ray/internal/domain"
	"github.com/mussy/simple-ray/internal/installer"
	"github.com/mussy/simple-ray/internal/lock"
	"github.com/mussy/simple-ray/internal/state"
	"github.com/mussy/simple-ray/internal/xui"
)

type API interface {
	ListClients(context.Context) ([]xui.ClientRecord, error)
	GetClient(context.Context, string) (xui.ClientRecord, error)
	AddClient(context.Context, xui.ClientCreate) error
	DeleteClient(context.Context, string) error
	ClientLinks(context.Context, string) ([]string, error)
	Status(context.Context) (xui.ServerStatus, error)
	GetDatabase(context.Context, io.Writer, int64) error
	ImportDatabase(context.Context, string, io.Reader) error
}

type APIFactory func(domain.State, domain.Secrets) (API, error)

type Service struct {
	Store      *state.Store
	LockPath   string
	APIFactory APIFactory
	Installer  *installer.Manager
}

func New(store *state.Store, lockPath string) *Service {
	return &Service{Store: store, LockPath: lockPath, Installer: installer.NewManager(store), APIFactory: func(s domain.State, secret domain.Secrets) (API, error) {
		base := fmt.Sprintf("http://127.0.0.1:%d/%s", s.PanelPort, strings.Trim(s.PanelBasePath, "/"))
		return xui.New(base, secret.APIToken)
	}}
}

func (s *Service) Install(ctx context.Context, request installer.Request) (installer.Result, error) {
	guard, err := lock.Acquire(s.LockPath, "install")
	if err != nil {
		return installer.Result{}, domain.E("LOCKED", "уже выполняется другая операция vpnctl", "Дождись её завершения и повтори", 3, err)
	}
	defer guard.Release()
	result, err := s.Installer.Install(ctx, request)
	if err != nil {
		return result, domain.E("INSTALL_FAILED", "не удалось установить VPN-сервер: "+err.Error(), "Исправь указанную проблему и повтори", 1, err)
	}
	if result.Existing {
		_, api, apiErr := s.api()
		if apiErr != nil {
			return result, apiErr
		}
		client, clientErr := api.GetClient(ctx, request.User)
		if clientErr != nil {
			var apiError *xui.APIError
			if !errors.As(clientErr, &apiError) || !isNotFoundMessage(apiError.Message) {
				return result, domain.E("API_UNAVAILABLE", "не удалось проверить существующего пользователя", "Запусти sudo vpnctl doctor", 1, clientErr)
			}
			client = xui.ClientRecord{Email: request.User, Enable: true, Flow: "xtls-rprx-vision"}
			if err := api.AddClient(ctx, xui.ClientCreate{Client: client, InboundIDs: []int64{result.State.InboundID}}); err != nil {
				return result, domain.E("USER_CREATE_FAILED", "установка работает, но пользователя создать не удалось", "Запусти sudo vpnctl doctor", 1, err)
			}
			client, clientErr = api.GetClient(ctx, request.User)
			if clientErr != nil {
				return result, domain.E("USER_VERIFY_FAILED", "пользователь создан, но проверить его не удалось", "Запусти sudo vpnctl doctor", 5, clientErr)
			}
		} else if len(client.InboundIDs) != 1 || client.InboundIDs[0] != result.State.InboundID {
			return result, domain.E("USER_CONFLICT", "пользователь существует вне управляемого подключения", "Выбери другое имя пользователя", 4, nil)
		}
		if len(client.InboundIDs) != 1 || client.InboundIDs[0] != result.State.InboundID {
			return result, domain.E("USER_CONFLICT", "пользователь подключён не только к управляемому подключению", "Выбери другое имя пользователя", 4, nil)
		}
		status, err := api.Status(ctx)
		if err != nil || !status.XrayRunning() {
			return result, domain.E("SERVICE_DEGRADED", "существующая установка не прошла проверку", "Запусти sudo vpnctl doctor", 5, err)
		}
	}
	return result, nil
}

func (s *Service) Uninstall(ctx context.Context, keepData, removeBackups bool) error {
	guard, err := lock.Acquire(s.LockPath, "uninstall")
	if err != nil {
		return domain.E("LOCKED", "уже выполняется другая операция vpnctl", "Дождись её завершения и повтори", 3, err)
	}
	defer guard.Release()
	installed, err := s.Store.LoadState()
	if err != nil {
		return domain.E("NOT_INSTALLED", "vpnctl не установлен", "Изменения не вносились", 4, err)
	}
	if err := s.Installer.Uninstall(ctx, installed, keepData, removeBackups); err != nil {
		return domain.E("UNINSTALL_FAILED", "безопасно удалить vpnctl не удалось", "Исправь конфликт владельца файлов и повтори", 1, err)
	}
	return nil
}

func (s *Service) api() (domain.State, API, error) {
	installation, err := s.Store.LoadState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installation, nil, domain.E("NOT_INSTALLED", "vpnctl не установлен", "Запусти sudo vpnctl install", 4, err)
		}
		return installation, nil, domain.E("STATE_UNREADABLE", "не удалось прочитать состояние vpnctl", "Проверь права файлов и запусти sudo vpnctl doctor", 1, err)
	}
	secrets, err := s.Store.LoadSecrets()
	if err != nil {
		return installation, nil, domain.E("SECRETS_UNREADABLE", "не удалось прочитать учётные данные vpnctl", "Восстанови проверенную резервную копию", 1, err)
	}
	api, err := s.APIFactory(installation, secrets)
	return installation, api, err
}

func (s *Service) Status(ctx context.Context) (domain.State, xui.ServerStatus, []domain.User, error) {
	installation, api, err := s.api()
	if err != nil {
		return installation, xui.ServerStatus{}, nil, err
	}
	server, serverErr := api.Status(ctx)
	clients, clientsErr := api.ListClients(ctx)
	if serverErr != nil || !server.XrayRunning() {
		return installation, server, nil, domain.E("SERVICE_DEGRADED", "3x-ui работает некорректно", "Запусти sudo vpnctl doctor", 5, serverErr)
	}
	if clientsErr != nil {
		return installation, server, nil, domain.E("SERVICE_DEGRADED", "не удалось прочитать VPN-пользователей", "Запусти sudo vpnctl doctor", 5, clientsErr)
	}
	return installation, server, toUsers(clients), nil
}

func (s *Service) AddUser(ctx context.Context, rawName string) (domain.User, bool, error) {
	name, err := domain.ValidateUserName(rawName)
	if err != nil {
		return domain.User{}, false, domain.E("INVALID_USER_NAME", "некорректное имя VPN-пользователя", err.Error(), 2, err)
	}
	guard, err := lock.Acquire(s.LockPath, "user-add")
	if err != nil {
		return domain.User{}, false, domain.E("LOCKED", "уже выполняется другая операция vpnctl", "Дождись её завершения и повтори", 3, err)
	}
	defer guard.Release()
	installation, api, err := s.api()
	if err != nil {
		return domain.User{}, false, err
	}
	existing, err := api.GetClient(ctx, name)
	if err == nil {
		if (existing.Flow != "" && existing.Flow != "xtls-rprx-vision") || !containsInbound(existing.InboundIDs, installation.InboundID) {
			return domain.User{}, false, domain.E("USER_CONFLICT", fmt.Sprintf("VPN-пользователь %q уже существует с несовместимыми настройками", name), "Выбери другое имя", 4, nil)
		}
		return toUser(existing), false, nil
	}
	var apiError *xui.APIError
	if !errors.As(err, &apiError) || (apiError.Status != http.StatusNotFound && !isNotFoundMessage(apiError.Message)) {
		return domain.User{}, false, domain.E("API_UNAVAILABLE", "не удалось проверить VPN-пользователя", "Запусти sudo vpnctl doctor", 1, err)
	}
	record := xui.ClientRecord{Email: name, Enable: true, Flow: "xtls-rprx-vision"}
	if err := api.AddClient(ctx, xui.ClientCreate{Client: record, InboundIDs: []int64{installation.InboundID}}); err != nil {
		return domain.User{}, false, domain.E("USER_CREATE_FAILED", fmt.Sprintf("не удалось создать VPN-пользователя %q", name), "Повтори или запусти sudo vpnctl doctor", 1, err)
	}
	created, err := api.GetClient(ctx, name)
	if err != nil {
		return domain.User{}, false, domain.E("USER_VERIFY_FAILED", "VPN-пользователь создан, но проверить его не удалось", "Запусти sudo vpnctl doctor", 5, err)
	}
	if len(created.InboundIDs) != 1 || created.InboundIDs[0] != installation.InboundID {
		return domain.User{}, false, domain.E("USER_VERIFY_FAILED", "не удалось проверить подключение VPN-пользователя", "Запусти sudo vpnctl doctor", 5, nil)
	}
	return toUser(created), true, nil
}

func (s *Service) RemoveUser(ctx context.Context, rawName string) (int, error) {
	name, err := domain.ValidateUserName(rawName)
	if err != nil {
		return 0, domain.E("INVALID_USER_NAME", "некорректное имя VPN-пользователя", err.Error(), 2, err)
	}
	guard, err := lock.Acquire(s.LockPath, "user-remove")
	if err != nil {
		return 0, domain.E("LOCKED", "уже выполняется другая операция vpnctl", "Дождись её завершения и повтори", 3, err)
	}
	defer guard.Release()
	installation, api, err := s.api()
	if err != nil {
		return 0, err
	}
	existing, err := api.GetClient(ctx, name)
	if err != nil {
		if !isClientNotFound(err) {
			return 0, domain.E("API_UNAVAILABLE", "не удалось проверить VPN-пользователя", "Запусти sudo vpnctl doctor", 1, err)
		}
		return 0, domain.E("USER_NOT_FOUND", fmt.Sprintf("VPN-пользователь %q не найден", name), "Посмотри список: sudo vpnctl user list", 4, err)
	}
	if !containsInbound(existing.InboundIDs, installation.InboundID) {
		return 0, domain.E("USER_CONFLICT", fmt.Sprintf("VPN-пользователь %q не принадлежит vpnctl", name), "Управляй им через его исходное подключение", 4, nil)
	}
	if len(existing.InboundIDs) != 1 {
		return 0, domain.E("USER_CONFLICT", fmt.Sprintf("VPN-пользователь %q подключён к нескольким подключениям", name), "Отключи его от vpnctl через проверенную процедуру", 4, nil)
	}
	if err := api.DeleteClient(ctx, name); err != nil {
		return 0, domain.E("USER_REMOVE_FAILED", fmt.Sprintf("не удалось удалить VPN-пользователя %q", name), "Запусти sudo vpnctl doctor", 1, err)
	}
	clients, err := api.ListClients(ctx)
	if err != nil {
		return 0, domain.E("USER_VERIFY_FAILED", "VPN-пользователь удалён, но проверить результат не удалось", "Запусти sudo vpnctl doctor", 5, err)
	}
	return len(toUsersForInbound(clients, installation.InboundID)), nil
}

func (s *Service) ListUsers(ctx context.Context) ([]domain.User, error) {
	installation, api, err := s.api()
	if err != nil {
		return nil, err
	}
	clients, err := api.ListClients(ctx)
	if err != nil {
		return nil, domain.E("API_UNAVAILABLE", "не удалось прочитать VPN-пользователей", "Запусти sudo vpnctl doctor", 1, err)
	}
	return toUsersForInbound(clients, installation.InboundID), nil
}

func (s *Service) UserLink(ctx context.Context, rawName string) (domain.User, string, error) {
	name, err := domain.ValidateUserName(rawName)
	if err != nil {
		return domain.User{}, "", domain.E("INVALID_USER_NAME", "некорректное имя VPN-пользователя", err.Error(), 2, err)
	}
	installation, api, err := s.api()
	if err != nil {
		return domain.User{}, "", err
	}
	record, err := api.GetClient(ctx, name)
	if err != nil {
		if !isClientNotFound(err) {
			return domain.User{}, "", domain.E("API_UNAVAILABLE", "не удалось проверить VPN-пользователя", "Запусти sudo vpnctl doctor", 1, err)
		}
		return domain.User{}, "", domain.E("USER_NOT_FOUND", fmt.Sprintf("VPN-пользователь %q не найден", name), "Посмотри список: sudo vpnctl user list", 4, err)
	}
	if !containsInbound(record.InboundIDs, installation.InboundID) {
		return domain.User{}, "", domain.E("USER_CONFLICT", fmt.Sprintf("VPN-пользователь %q не принадлежит vpnctl", name), "Управляй им через его исходное подключение", 4, nil)
	}
	links, err := api.ClientLinks(ctx, name)
	if err != nil {
		return domain.User{}, "", domain.E("LINK_UNAVAILABLE", "не удалось создать ссылку VPN-клиента", "Запусти sudo vpnctl doctor", 1, err)
	}
	if len(links) == 0 {
		return domain.User{}, "", domain.E("LINK_UNAVAILABLE", "не удалось создать ссылку VPN-клиента", "Запусти sudo vpnctl doctor", 1, nil)
	}
	return toUser(record), links[0], nil
}

func (s *Service) Backup(ctx context.Context, destination string, plaintextAcknowledged bool) (backup.Result, error) {
	guard, err := lock.Acquire(s.LockPath, "backup")
	if err != nil {
		return backup.Result{}, domain.E("LOCKED", "уже выполняется другая операция vpnctl", "Дождись её завершения и повтори", 3, err)
	}
	defer guard.Release()
	installation, api, err := s.api()
	if err != nil {
		return backup.Result{}, err
	}
	secrets, err := s.Store.LoadSecrets()
	if err != nil {
		return backup.Result{}, err
	}
	if destination == "" {
		destination = filepath.Join(s.Store.BackupsDir(), "vpnctl-backup-"+time.Now().UTC().Format("20060102-150405")+".tar.gz")
	}
	result, err := backup.Create(ctx, api, installation, secrets, destination, plaintextAcknowledged)
	if err != nil {
		return backup.Result{}, domain.E("BACKUP_FAILED", "не удалось создать резервную копию", "Существующие копии не были перезаписаны", 1, err)
	}
	return result, nil
}

func (s *Service) Doctor(ctx context.Context) []domain.Check {
	checks := []domain.Check{{Group: "Система", Name: "состояние vpnctl", Status: "passed"}}
	installation, api, err := s.api()
	if err != nil {
		checks[0].Status = "failed"
		checks[0].Reason = err.Error()
		return checks
	}
	checks = append(checks,
		domain.Check{Group: "Сеть", Name: "панель доступна только локально", Status: boolStatus(installation.PanelListen == "127.0.0.1")},
		domain.Check{Group: "Конфигурация", Name: "подключение VLESS Reality", Status: boolStatus(installation.InboundID > 0 && installation.RealitySNI != "")},
	)
	status, err := api.Status(ctx)
	if err != nil {
		checks = append(checks, domain.Check{Group: "Сервисы", Name: "API 3x-ui", Status: "failed", Reason: "локальный API недоступен"})
	} else {
		checks = append(checks,
			domain.Check{Group: "Сервисы", Name: "API 3x-ui", Status: "passed"},
			domain.Check{Group: "Сервисы", Name: "Xray", Status: boolStatus(status.XrayRunning())},
		)
	}
	return checks
}

func boolStatus(value bool) string {
	if value {
		return "passed"
	}
	return "failed"
}

func toUsers(records []xui.ClientRecord) []domain.User {
	users := make([]domain.User, 0, len(records))
	for _, record := range records {
		users = append(users, toUser(record))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	return users
}

func toUsersForInbound(records []xui.ClientRecord, inboundID int64) []domain.User {
	filtered := make([]xui.ClientRecord, 0, len(records))
	for _, record := range records {
		if containsInbound(record.InboundIDs, inboundID) {
			filtered = append(filtered, record)
		}
	}
	return toUsers(filtered)
}

func containsInbound(inboundIDs []int64, expected int64) bool {
	for _, inboundID := range inboundIDs {
		if inboundID == expected {
			return true
		}
	}
	return false
}

func toUser(record xui.ClientRecord) domain.User {
	return domain.User{Name: record.Email, Enabled: record.Enable, ExpiryTime: record.ExpiryTime}
}

func isNotFoundMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "not found") || strings.Contains(message, "does not exist")
}

func isClientNotFound(err error) bool {
	var apiError *xui.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	return apiError.Status == http.StatusNotFound || (apiError.Status == 0 && isNotFoundMessage(apiError.Message))
}

func panelURL(s domain.State) string {
	return (&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", s.PanelPort), Path: s.PanelBasePath}).String()
}
