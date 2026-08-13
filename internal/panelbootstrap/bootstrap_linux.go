//go:build linux

package panelbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"time"
)

var namespacePattern = regexp.MustCompile(`^/run/netns/vpnctl-[a-f0-9]{16}$`)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"msg"`
	Object  json.RawMessage `json:"obj"`
}

func Run(ctx context.Context, namespace string, input io.Reader) error {
	if os.Geteuid() != 0 {
		return errors.New("panel bootstrap requires root")
	}
	if !namespacePattern.MatchString(namespace) {
		return errors.New("invalid bootstrap network namespace")
	}
	var secret credentials
	decoder := json.NewDecoder(io.LimitReader(input, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&secret); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid bootstrap credentials")
	}
	if len(secret.Username) < 20 || len(secret.Password) < 32 || len(secret.Username) > 64 || len(secret.Password) > 128 {
		return errors.New("invalid bootstrap credential length")
	}
	ns, err := os.Stat(namespace)
	if err != nil {
		return fmt.Errorf("open bootstrap namespace: %w", err)
	}
	current, err := os.Stat("/proc/self/ns/net")
	if err != nil || !os.SameFile(ns, current) {
		return errors.New("bootstrap helper is outside the isolated network namespace")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	base, _ := url.Parse("http://127.0.0.1:2053/")
	csrf, err := waitCSRF(ctx, client, base)
	if err != nil {
		return err
	}
	if err := postJSON(ctx, client, base.ResolveReference(&url.URL{Path: "login"}).String(), csrf, map[string]string{"username": "admin", "password": "admin", "twoFactorCode": ""}); err != nil {
		return fmt.Errorf("authenticate isolated bootstrap panel: %w", err)
	}
	if err := postJSON(ctx, client, base.ResolveReference(&url.URL{Path: "panel/api/setting/updateUser"}).String(), csrf, map[string]string{"oldUsername": "admin", "oldPassword": "admin", "newUsername": secret.Username, "newPassword": secret.Password, "twoFactorCode": ""}); err != nil {
		return fmt.Errorf("secure isolated bootstrap panel: %w", err)
	}
	return nil
}

func waitCSRF(ctx context.Context, client *http.Client, base *url.URL) (string, error) {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.ResolveReference(&url.URL{Path: "csrf-token"}).String(), nil)
		resp, err := client.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4097))
			resp.Body.Close()
			var result envelope
			if readErr == nil && len(data) <= 4096 && resp.StatusCode == http.StatusOK && json.Unmarshal(data, &result) == nil && result.Success {
				var token string
				if json.Unmarshal(result.Object, &token) == nil && token != "" {
					return token, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", errors.New("isolated bootstrap panel did not become ready")
		case <-ticker.C:
		}
	}
}

func postJSON(ctx context.Context, client *http.Client, endpoint, csrf string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(data) > 4096 || resp.StatusCode != http.StatusOK {
		return errors.New("panel returned an invalid response")
	}
	var result envelope
	if json.Unmarshal(data, &result) != nil || !result.Success {
		return errors.New("panel rejected bootstrap request")
	}
	return nil
}
