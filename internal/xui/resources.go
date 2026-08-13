package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type ClientRecord struct {
	ID         string  `json:"id,omitempty"`
	Email      string  `json:"email"`
	Enable     bool    `json:"enable"`
	ExpiryTime int64   `json:"expiryTime"`
	Flow       string  `json:"flow"`
	InboundIDs []int64 `json:"inboundIds,omitempty"`
}

type ClientCreate struct {
	Client     ClientRecord `json:"client"`
	InboundIDs []int64      `json:"inboundIds"`
}

type Inbound struct {
	ID             int64           `json:"id,omitempty"`
	Enable         bool            `json:"enable"`
	Remark         string          `json:"remark"`
	Listen         string          `json:"listen"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	ExpiryTime     int64           `json:"expiryTime"`
	Total          int64           `json:"total"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings json.RawMessage `json:"streamSettings"`
	Sniffing       json.RawMessage `json:"sniffing"`
}

type ServerStatus struct {
	CPU       float64 `json:"cpu"`
	Memory    int64   `json:"mem"`
	XrayState bool    `json:"xrayState"`
}

type KeyPair struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

var clientNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func (c *Client) ListClients(ctx context.Context) ([]ClientRecord, error) {
	var result []ClientRecord
	err := c.doJSON(ctx, http.MethodGet, nil, &result, "clients", "list")
	if err == nil {
		for _, record := range result {
			if !clientNamePattern.MatchString(record.Email) {
				return nil, fmt.Errorf("3x-ui returned an invalid client name")
			}
		}
	}
	return result, err
}

func (c *Client) GetClient(ctx context.Context, name string) (ClientRecord, error) {
	var result ClientRecord
	err := c.doJSON(ctx, http.MethodGet, nil, &result, "clients", "get", url.PathEscape(name))
	if err == nil {
		if result.Email == "" {
			err = &APIError{Message: "client not found"}
		} else if !clientNamePattern.MatchString(result.Email) || result.Email != name {
			err = fmt.Errorf("3x-ui returned an invalid client identity")
		}
	}
	return result, err
}

func (c *Client) AddClient(ctx context.Context, request ClientCreate) error {
	return c.doJSON(ctx, http.MethodPost, request, nil, "clients", "add")
}

func (c *Client) DeleteClient(ctx context.Context, name string) error {
	u := c.endpoint("clients", "del", url.PathEscape(name)) + "?keepTraffic=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		return errorsNewMalformed()
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponse+1))
	if err != nil || len(data) > maxJSONResponse {
		return errorsNewMalformed()
	}
	var env envelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&env); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errorsNewMalformed()
	}
	if !env.Success {
		return &APIError{Message: env.Message}
	}
	return nil
}

func errorsNewMalformed() error { return fmt.Errorf("3x-ui API returned malformed JSON") }

func (c *Client) ClientLinks(ctx context.Context, name string) ([]string, error) {
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, nil, &raw, "clients", "links", url.PathEscape(name)); err != nil {
		return nil, err
	}
	var links []string
	if err := json.Unmarshal(raw, &links); err == nil {
		return filterLinks(links)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		for _, value := range object {
			var link string
			if json.Unmarshal(value, &link) == nil {
				links = append(links, link)
			}
		}
		return filterLinks(links)
	}
	var link string
	if err := json.Unmarshal(raw, &link); err == nil {
		return filterLinks([]string{link})
	}
	return nil, fmt.Errorf("3x-ui API returned incompatible client links")
}

func filterLinks(links []string) ([]string, error) {
	result := make([]string, 0, len(links))
	for _, link := range links {
		if len(link) > 8192 {
			continue
		}
		if strings.IndexFunc(link, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
			continue
		}
		parsed, err := url.Parse(link)
		if err != nil || parsed.Scheme != "vless" || parsed.User == nil || parsed.User.Username() == "" || parsed.Host == "" {
			continue
		}
		result = append(result, link)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("3x-ui did not return a VLESS link")
	}
	return result, nil
}

func (c *Client) ListInbounds(ctx context.Context) ([]Inbound, error) {
	var result []Inbound
	err := c.doJSON(ctx, http.MethodGet, nil, &result, "inbounds", "list")
	return result, err
}

func (c *Client) GetInbound(ctx context.Context, id int64) (Inbound, error) {
	var result Inbound
	err := c.doJSON(ctx, http.MethodGet, nil, &result, "inbounds", "get", fmt.Sprint(id))
	return result, err
}

func (c *Client) AddInbound(ctx context.Context, inbound any) (Inbound, error) {
	var result Inbound
	err := c.doJSON(ctx, http.MethodPost, inbound, &result, "inbounds", "add")
	return result, err
}

func (c *Client) Status(ctx context.Context) (ServerStatus, error) {
	var result ServerStatus
	err := c.doJSON(ctx, http.MethodGet, nil, &result, "server", "status")
	return result, err
}

func (c *Client) NewX25519(ctx context.Context) (KeyPair, error) {
	var result KeyPair
	err := c.doJSON(ctx, http.MethodGet, nil, &result, "server", "getNewX25519Cert")
	if err == nil && (result.PrivateKey == "" || result.PublicKey == "") {
		err = fmt.Errorf("3x-ui returned an incomplete Reality key pair")
	}
	return result, err
}
