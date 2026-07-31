// Package cf is a small client for the Cloudflare API, limited to what running
// a Cloudflare Tunnel in front of a homelab needs: create the tunnel, keep its
// ingress table, and point a hostname at it.
//
// It is deliberately not a general-purpose SDK. Every path it can call is
// listed in endpoints.go and documented in docs/CF-API-MAP.md, for the same
// reason the PVE client works that way — an endpoint invented from memory is a
// 404 discovered against production.
package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the v4 API root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// EnvToken and EnvAccount are where the credentials come from. Never a flag:
// an API token passed as an argument is visible to every process on the machine
// through `ps`.
const (
	EnvToken   = "CF_API_TOKEN"
	EnvAccount = "CF_ACCOUNT_ID"
)

// Client talks to the Cloudflare API on behalf of one account.
type Client struct {
	base    string
	token   string
	account string
	http    *http.Client
}

// Options configures a client.
type Options struct {
	Token     string
	AccountID string
	BaseURL   string
	Timeout   time.Duration
}

// New builds a client, refusing the two mistakes that produce a confusing 403
// much later: no token, and no account.
func New(o Options) (*Client, error) {
	if o.Token == "" {
		return nil, fmt.Errorf(`aucun jeton d'API Cloudflare.

  export %s="$(security find-generic-password -s pvecli-cloudflare -a api-token -w)"

Le jeton doit porter, sur le compte visé : Cloudflare Tunnel:Edit,
et sur la zone : DNS:Edit`, EnvToken)
	}
	if o.AccountID == "" {
		return nil, fmt.Errorf("aucun identifiant de compte Cloudflare : %s, ou « pvecli config set cf.account_id »", EnvAccount)
	}
	base := o.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		base:    strings.TrimSuffix(base, "/"),
		token:   o.Token,
		account: o.AccountID,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

// AccountID is the account this client acts on.
func (c *Client) AccountID() string { return c.account }

// APIError is a refusal Cloudflare explained. Its codes are worth keeping: 1000
// is "invalid credentials", 7003 is "route not found", and telling them apart
// saves an hour.
type APIError struct {
	Status int
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
}

func (e *APIError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("Cloudflare a répondu %d, sans détail", e.Status)
	}
	parts := make([]string, 0, len(e.Errors))
	for _, x := range e.Errors {
		parts = append(parts, fmt.Sprintf("%d %s", x.Code, x.Message))
	}
	return "Cloudflare a refusé : " + strings.Join(parts, " ; ")
}

// envelope is the shape every v4 response takes.
//
// `success` is what has to be read, not the HTTP status: the API answers 200
// with success=false often enough that trusting the status code is how a
// failure gets reported as a result.
type envelope struct {
	Success bool            `json:"success"`
	Errors  json.RawMessage `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

func (c *Client) do(ctx context.Context, e endpoint, values []string, query url.Values, body, out any) error {
	path := e.path(values...)
	full := c.base + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, e.Method, full, reader)
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
		return fmt.Errorf("%s %s : %w", e.Method, path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s %s : réponse illisible (HTTP %d)", e.Method, path, resp.StatusCode)
	}
	if !env.Success {
		apiErr := &APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(env.Errors, &apiErr.Errors)
		return apiErr
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// Verify checks the token before anything is written, so a bad credential is
// reported as a bad credential rather than as a failed tunnel creation.
func (c *Client) Verify(ctx context.Context) error {
	return c.do(ctx, epTokenVerify, nil, nil, nil, nil)
}
