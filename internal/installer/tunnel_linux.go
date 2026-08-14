//go:build linux

package installer

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mussy/simple-ray/internal/domain"
)

// echoEndpoints report the caller's public address as a bare IP in the body.
// Several are tried so one unreachable third party cannot make a healthy
// tunnel look broken.
var echoEndpoints = []struct{ Host, Path string }{
	{Host: "api.ipify.org", Path: "/"},
	{Host: "icanhazip.com", Path: "/"},
	{Host: "ifconfig.me", Path: "/ip"},
}

// ProbeClientLink runs a throwaway Xray client built from the exact link a VPN
// user is given, pushes a real request through it and returns the public IP
// observed on the far side. It is the only check that proves the tunnel both
// authenticates and forwards traffic; a Reality mismatch makes the server
// relay the connection to its decoy site, which looks like a healthy
// connection to every other check.
func ProbeClientLink(ctx context.Context, binary string, link domain.ClientLink) (string, error) {
	if binary == "" {
		return "", errors.New("путь к Xray не задан")
	}
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("исполняемый файл Xray недоступен: %w", err)
	}
	if link.UUID == "" || link.Address == "" || link.Port < 1 || link.Port > 65535 || link.SNI == "" || link.PublicKey == "" {
		return "", errors.New("в ссылке не хватает параметров подключения")
	}

	ports, err := reserveLoopbackPorts(len(echoEndpoints))
	if err != nil {
		return "", err
	}
	configPath, cleanup, err := writeProbeConfig(link, ports)
	if err != nil {
		return "", err
	}
	defer cleanup()

	runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, "runuser", "--user", serviceUser, "--", binary, "-c", configPath)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("не удалось запустить проверочный клиент Xray: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	defer func() {
		if stopped {
			return
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()

	if err := waitForListener(runCtx, ports[0], done, &stopped); err != nil {
		return "", err
	}

	var failures []string
	for index, endpoint := range echoEndpoints {
		address, err := fetchEchoIP(runCtx, ports[index], endpoint.Host, endpoint.Path)
		if err == nil {
			return address, nil
		}
		failures = append(failures, endpoint.Host+": "+err.Error())
		if runCtx.Err() != nil {
			break
		}
	}
	return "", fmt.Errorf("трафик через туннель не прошёл (%s)", strings.Join(failures, "; "))
}

// verifyRealityTunnel proves a freshly installed inbound carries traffic. It
// builds the same link the user will receive so installation validates exactly
// what is handed out.
//
// Its reach stops at the server: the probe dials the server's own public
// address, which the kernel routes over loopback, so it never exercises the
// path, MTU or DPI a phone meets. A green result here means the server side is
// sound, not that every client app can reach it.
func verifyRealityTunnel(ctx context.Context, binary string, link domain.ClientLink) error {
	observed, err := ProbeClientLink(ctx, binary, link)
	if err != nil {
		return err
	}
	if net.ParseIP(observed) == nil {
		return errors.New("tunnel returned an invalid public address response")
	}
	return nil
}

func reserveLoopbackPorts(count int) ([]int, error) {
	ports := make([]int, 0, count)
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for len(ports) < count {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}

func writeProbeConfig(link domain.ClientLink, ports []int) (string, func(), error) {
	inbounds := make([]any, 0, len(ports))
	for index, port := range ports {
		inbounds = append(inbounds, map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": echoEndpoints[index].Host, "port": 443, "network": "tcp"},
		})
	}
	users := map[string]any{"id": link.UUID, "encryption": "none"}
	if link.Flow != "" {
		users["flow"] = link.Flow
	}
	config := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": inbounds,
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": link.Address, "port": link.Port, "users": []any{users},
			}}},
			"streamSettings": map[string]any{
				"network": link.TransportNetwork(), "security": "reality",
				"realitySettings": map[string]any{
					"serverName": link.SNI, "fingerprint": link.Fingerprint,
					"password": link.PublicKey, "shortId": link.ShortID, "spiderX": link.SpiderX,
				},
			},
		}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", nil, err
	}
	suffix, err := randomHex(8)
	if err != nil {
		return "", nil, err
	}
	configPath := filepath.Join(programDir, ".vpnctl-client-check-"+suffix+".json")
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(configPath)
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(configPath) }
	account, err := user.Lookup(serviceUser)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil || os.Chown(configPath, uid, gid) != nil {
		cleanup()
		return "", nil, errors.New("temporary client configuration ownership could not be set")
	}
	return configPath, cleanup, nil
}

func waitForListener(ctx context.Context, port int, done chan error, stopped *bool) error {
	endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		connection, err := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(ctx, "tcp", endpoint)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case processErr := <-done:
			*stopped = true
			return fmt.Errorf("проверочный клиент Xray завершился: %w", processErr)
		case <-ctx.Done():
			return errors.New("проверочный клиент Xray не успел запуститься")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func fetchEchoIP(ctx context.Context, port int, host, path string) (string, error) {
	endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(12 * time.Second))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "vpnctl-doctor")
	if err := request.Write(connection); err != nil {
		return "", err
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ответ %d", response.StatusCode)
	}
	value := strings.TrimSpace(string(body))
	if net.ParseIP(value) == nil {
		return "", errors.New("получен не IP-адрес")
	}
	return value, nil
}
