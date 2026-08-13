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
)

func verifyRealityTunnel(ctx context.Context, binary, clientID, address string, serverPort int, serverName, password, shortID string) error {
	if clientID == "" || net.ParseIP(address) == nil || serverPort < 1 || serverPort > 65535 || serverName == "" || password == "" || shortID == "" {
		return errors.New("client check parameters are incomplete")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	clientPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return err
	}
	config := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": clientPort, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": "api.ipify.org", "port": 443, "network": "tcp"},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": address, "port": serverPort,
				"users": []any{map[string]any{"id": clientID, "encryption": "none", "flow": "xtls-rprx-vision"}},
			}}},
			"streamSettings": map[string]any{
				"network": "raw", "security": "reality",
				"realitySettings": map[string]any{"serverName": serverName, "fingerprint": "chrome", "password": password, "shortId": shortID, "spiderX": "/"},
			},
		}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	configPath := filepath.Join(programDir, ".vpnctl-client-check-"+suffix+".json")
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
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
		return err
	}
	defer os.Remove(configPath)
	account, err := user.Lookup(serviceUser)
	if err != nil {
		return err
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil || os.Chown(configPath, uid, gid) != nil {
		return errors.New("temporary client configuration ownership could not be set")
	}

	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(checkCtx, "runuser", "--user", serviceUser, "--", binary, "-c", configPath)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	defer func() {
		if waited {
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

	endpoint := net.JoinHostPort("127.0.0.1", fmt.Sprint(clientPort))
	for {
		connection, dialErr := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(checkCtx, "tcp", endpoint)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		select {
		case processErr := <-done:
			waited = true
			return fmt.Errorf("temporary Xray client stopped: %w", processErr)
		case <-checkCtx.Done():
			return checkCtx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	tlsConnection, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: "api.ipify.org", MinVersion: tls.VersionTLS12}}).DialContext(checkCtx, "tcp", endpoint)
	if err != nil {
		return err
	}
	defer tlsConnection.Close()
	_ = tlsConnection.SetDeadline(time.Now().Add(10 * time.Second))
	request, _ := http.NewRequestWithContext(checkCtx, http.MethodGet, "https://api.ipify.org/", nil)
	if err := request.Write(tlsConnection); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(tlsConnection), request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 65))
	if err != nil || response.StatusCode != http.StatusOK || net.ParseIP(strings.TrimSpace(string(body))) == nil {
		return errors.New("tunnel returned an invalid public address response")
	}
	return nil
}
