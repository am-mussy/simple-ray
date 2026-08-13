package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxJSONResponse = 2 << 20

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"msg"`
	Object  json.RawMessage `json:"obj"`
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("3x-ui API returned status %d: %s", e.Status, e.Message)
	}
	return "3x-ui API rejected request: " + e.Message
}

func New(baseURL, token string) (*Client, error) {
	if token == "" {
		return nil, errors.New("API token is empty")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("API URL must be a plain loopback HTTP URL")
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	if address == nil || !address.IsLoopback() || parsed.Port() == "" {
		return nil, errors.New("API URL must use a loopback IP and explicit port")
	}
	parsed.Path = "/" + strings.Trim(parsed.Path, "/")
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}
	return &Client{
		base:  parsed,
		token: token,
		http:  &http.Client{Transport: transport, Timeout: 10 * time.Second},
	}, nil
}

func NewWithHTTPClient(baseURL, token string, client *http.Client) (*Client, error) {
	c, err := New(baseURL, token)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("HTTP client is nil")
	}
	c.http = client
	return c, nil
}

func (c *Client) endpoint(parts ...string) string {
	u := *c.base
	all := append([]string{c.base.Path, "panel", "api"}, parts...)
	u.Path = path.Join(all...)
	return u.String()
}

func (c *Client) doJSON(ctx context.Context, method string, body any, result any, parts ...string) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(parts...), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("local 3x-ui API request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxJSONResponse))
		return &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("3x-ui API returned an unexpected content type")
	}
	limited := io.LimitReader(resp.Body, maxJSONResponse+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxJSONResponse {
		return errors.New("3x-ui API response exceeded size limit")
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return errors.New("3x-ui API returned malformed JSON")
	}
	if !env.Success {
		message := strings.TrimSpace(env.Message)
		if message == "" {
			message = "operation failed"
		}
		return &APIError{Message: message}
	}
	if result != nil && len(env.Object) > 0 && string(env.Object) != "null" {
		if err := json.Unmarshal(env.Object, result); err != nil {
			return errors.New("3x-ui API returned an incompatible response")
		}
	}
	return nil
}

func (c *Client) GetDatabase(ctx context.Context, dst io.Writer, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("server", "getDb"), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("download database backup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	if resp.ContentLength > maxBytes {
		return errors.New("database backup exceeds size limit")
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return errors.New("database backup exceeds size limit")
	}
	return nil
}

func (c *Client) ImportDatabase(ctx context.Context, name string, source io.Reader) error {
	const maxImportBytes = 128 << 20
	database, err := io.ReadAll(io.LimitReader(source, maxImportBytes+1))
	if err != nil {
		return err
	}
	if len(database) > maxImportBytes {
		return errors.New("database restore exceeds size limit")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("db", path.Base(name))
	if err != nil {
		return err
	}
	if _, err := part.Write(database); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("server", "importDB"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("restore database: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponse+1))
	if err != nil || len(data) > maxJSONResponse {
		return errors.New("invalid restore response")
	}
	var env envelope
	if json.Unmarshal(data, &env) != nil || !env.Success {
		return errors.New("3x-ui rejected database restore")
	}
	return nil
}
