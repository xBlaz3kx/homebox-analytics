// Package homebox is a client for the Homebox REST API (sysadminsmedia/homebox >= 0.27, which
// exposes items as "entities" and labels as "tags").
package homebox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Config struct {
	URL string
	// APIKey takes precedence over Username/Password.
	APIKey     string
	Username   string
	Password   string
	Timeout    time.Duration
	HTTPClient *http.Client
}

type Client struct {
	baseURL  *url.URL
	http     *http.Client
	apiKey   string
	username string
	password string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// ErrNotFound is returned when Homebox responds with 404.
var ErrNotFound = errors.New("homebox: not found")

// APIError is a non-2xx response from Homebox.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("homebox: status %d: %s", e.StatusCode, e.Body)
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(cfg.URL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse homebox url: %w", err)
	}
	if cfg.APIKey == "" && (cfg.Username == "" || cfg.Password == "") {
		return nil, errors.New("homebox: api key or username/password required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: cfg.Timeout}
	}

	return &Client{baseURL: u, http: httpClient, apiKey: cfg.APIKey, username: cfg.Username, password: cfg.Password}, nil
}

// Ping checks that Homebox is reachable.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/api/v1/status", nil, nil, nil, false)
}

// ListOptions filters entity listing. Page is 1-based.
type ListOptions struct {
	Query    string
	TagIDs   []string
	Page     int
	PageSize int
}

func (c *Client) ListEntities(ctx context.Context, opts ListOptions) (*EntityPage, error) {
	q := url.Values{}
	if opts.Query != "" {
		q.Set("q", opts.Query)
	}
	for _, id := range opts.TagIDs {
		q.Add("tags", id)
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		q.Set("pageSize", strconv.Itoa(opts.PageSize))
	}

	var page EntityPage
	if err := c.do(ctx, http.MethodGet, "/api/v1/entities", q, nil, &page, true); err != nil {
		return nil, err
	}
	return &page, nil
}

// ListAllEntities pages through all (non-location) entities, optionally restricted to tags (OR).
func (c *Client) ListAllEntities(ctx context.Context, tagIDs []string) ([]EntitySummary, error) {
	const pageSize = 100

	var all []EntitySummary
	for page := 1; ; page++ {
		res, err := c.ListEntities(ctx, ListOptions{TagIDs: tagIDs, Page: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if len(res.Items) < pageSize || (res.Total > 0 && len(all) >= res.Total) {
			return all, nil
		}
	}
}

func (c *Client) GetEntity(ctx context.Context, id string) (*Entity, error) {
	var e Entity
	if err := c.do(ctx, http.MethodGet, "/api/v1/entities/"+url.PathEscape(id), nil, nil, &e, true); err != nil {
		return nil, err
	}
	return &e, nil
}

// SetQuantity patches only the quantity of an entity.
func (c *Client) SetQuantity(ctx context.Context, id string, quantity float64) (*Entity, error) {
	body := struct {
		ID       string  `json:"id"`
		Quantity float64 `json:"quantity"`
	}{ID: id, Quantity: quantity}

	var e Entity
	if err := c.do(ctx, http.MethodPatch, "/api/v1/entities/"+url.PathEscape(id), nil, body, &e, true); err != nil {
		return nil, err
	}
	return &e, nil
}

func (c *Client) ListTags(ctx context.Context) ([]Tag, error) {
	var tags []Tag
	if err := c.do(ctx, http.MethodGet, "/api/v1/tags", nil, nil, &tags, true); err != nil {
		return nil, err
	}
	return tags, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any, auth bool) error {
	err := c.doOnce(ctx, method, path, query, in, out, auth)

	// A session token may have been revoked server-side; log in again once.
	var apiErr *APIError
	if auth && c.apiKey == "" && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		return c.doOnce(ctx, method, path, query, in, out, auth)
	}
	return err
}

func (c *Client) doOnce(ctx context.Context, method, path string, query url.Values, in, out any, auth bool) error {
	u := *c.baseURL
	u.Path += path
	u.RawQuery = query.Encode()

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		token, err := c.authorization(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("homebox %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode homebox %s %s: %w", method, path, err)
	}
	return nil
}

// authorization returns the Authorization header value, logging in when needed.
func (c *Client) authorization(ctx context.Context) (string, error) {
	if c.apiKey != "" {
		return bearer(c.apiKey), nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Until(c.expiresAt) > time.Minute {
		return c.token, nil
	}

	form := struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		StayLoggedIn bool   `json:"stayLoggedIn"`
	}{Username: c.username, Password: c.password, StayLoggedIn: true}

	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := c.doOnce(ctx, http.MethodPost, "/api/v1/users/login", nil, form, &tok, false); err != nil {
		return "", fmt.Errorf("homebox login: %w", err)
	}

	c.token = bearer(tok.Token)
	c.expiresAt = tok.ExpiresAt
	if c.expiresAt.IsZero() {
		c.expiresAt = time.Now().Add(time.Hour)
	}
	return c.token, nil
}

// bearer prefixes the token unless Homebox already returned it with the scheme.
func bearer(token string) string {
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}
