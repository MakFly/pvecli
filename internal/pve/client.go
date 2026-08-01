// Package pve is the Proxmox VE API client: transport, PVEAPIToken
// authentication, typed models, and the translation of HTTP answers into
// actionable errors (PRD §7.2, §7.5).
//
// It is the only package allowed to speak net/http.
package pve

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// DefaultTimeout is the per-request budget of PRD §7.2. --timeout overrides it.
const DefaultTimeout = 30 * time.Second

// maxBody caps how much of an answer is read. A runaway response should fail
// the command, not the machine.
const maxBody = 32 << 20

// Options configures a Client.
type Options struct {
	Endpoint string
	TokenID  string
	Secret   string
	Timeout  time.Duration

	// Trust selects how the server certificate is verified (PRD §7.3).
	Trust TrustOptions

	// AccessClientID and AccessClientSecret authenticate this client to a
	// Cloudflare Access application placed in front of the node. Both or
	// neither: half a service token is refused at New().
	AccessClientID     string
	AccessClientSecret string

	// Trace, when set, receives every exchange. The client strips the
	// Authorization header before handing it over: no secret ever leaves this
	// package, whatever the tracer then does with the rest.
	Trace Tracer

	// Transport is the test seam: point it at an httptest.Server's transport
	// and the whole client works with no Proxmox node powered on. When set, it
	// takes precedence over Trust.
	Transport http.RoundTripper
}

// Client talks to one Proxmox VE node.
type Client struct {
	base    *url.URL
	tokenID string
	secret  string
	// Ticket authentication, used only by `login` to bootstrap a token on a
	// node we have none for. Empty everywhere else. See ticket.go.
	ticket       string
	csrf         string
	trust        TrustOptions
	accessID     string
	accessSecret string
	trace        Tracer
	http         *http.Client
}

// Tracer receives the details of each exchange. internal/log implements it.
type Tracer interface {
	Request(method, url string, header http.Header, body []byte)
	Response(status int, took time.Duration, header http.Header, body []byte)
}

// TrustMode reports how this client verifies the node, for commands that must
// say so out loud (`doctor`, the --insecure warning).
func (c *Client) TrustMode() TrustMode { return c.trust.Mode() }

// Endpoint returns the base URL the client talks to.
func (c *Client) Endpoint() string { return c.base.String() }

// TokenID returns the identity this client authenticates as — never its
// secret. `access whoami` needs it to say whether the caller is a token, and
// whether that token is privilege-separated.
func (c *Client) TokenID() string { return c.tokenID }

// New builds a client. It fails before any network call when something
// required is missing — a command should not open a socket only to discover it
// had nothing to authenticate with.
func New(o Options) (*Client, error) {
	if o.Endpoint == "" {
		return nil, fmt.Errorf("aucun endpoint configuré — lance « pvecli config init --endpoint https://…:8006 » ou exporte PVE_API_URL")
	}
	if o.TokenID == "" {
		return nil, &AuthError{
			Reason: "aucun identifiant de token configuré",
			Hint: "Définis-le dans la configuration ou dans l'environnement :\n" +
				"  pvecli config set token_id 'automation@pve!pvectl'\n" +
				"  export PVE_API_TOKEN_ID='automation@pve!pvectl'",
		}
	}
	if o.Secret == "" {
		return nil, &AuthError{
			Reason: "le secret du token d'API est absent de l'environnement",
			Hint: "Il ne peut venir que de là — jamais du fichier de configuration,\n" +
				"jamais d'un flag (un flag est visible dans « ps » et reste dans\n" +
				"l'historique du shell).\n\n" +
				"  export PVE_API_TOKEN_SECRET=\"…\"\n\n" +
				"Sur macOS, s'il est rangé dans le trousseau :\n" +
				"  export PVE_API_TOKEN_SECRET=\"$(security find-generic-password -a pvecli -s pvecli-token -w)\"",
		}
	}

	// Half a service token is worse than none: Cloudflare answers 403 to a
	// request it will not let through, and that 403 is indistinguishable from a
	// Proxmox permission error — which is where the hunt then starts.
	if (o.AccessClientID == "") != (o.AccessClientSecret == "") {
		missing, present := "CF_ACCESS_CLIENT_SECRET", "CF_ACCESS_CLIENT_ID"
		if o.AccessClientID == "" {
			missing, present = present, missing
		}
		return nil, &AuthError{
			Reason: "service token Cloudflare Access incomplet : " + present + " est défini, " + missing + " manque",
			Hint: "Les deux vont ensemble. Avec un seul, Cloudflare refuse la requête\n" +
				"par un 403 qui ressemble à un refus de Proxmox.\n\n" +
				"  export " + missing + "=\"…\"\n\n" +
				"Ou retire l'autre pour joindre le nœud directement, sans passer par Access.",
		}
	}

	base, err := normalizeBase(o.Endpoint)
	if err != nil {
		return nil, err
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	transport := o.Transport
	if transport == nil {
		tc, err := tlsConfig(o.Trust, base.Host)
		if err != nil {
			return nil, err
		}
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = tc
		transport = t
	}

	return &Client{
		base:         base,
		tokenID:      o.TokenID,
		secret:       o.Secret,
		trust:        o.Trust,
		accessID:     o.AccessClientID,
		accessSecret: o.AccessClientSecret,
		trace:        o.Trace,
		http:         &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// normalizeBase accepts both forms of endpoint people actually have on hand —
// "https://host:8006" and "https://host:8006/api2/json" — and always returns
// the second.
func normalizeBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("endpoint %q illisible : %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q incomplet — attendu https://hôte:8006", raw)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api2/json") + "/api2/json"
	return u, nil
}

// get issues a request for a declared endpoint and decodes the payload into
// out, which may be nil when only the success matters.
//
// Unexported on purpose: callers outside this package cannot invent a path,
// they can only call a method that declares one (see endpoints.go).
func (c *Client) get(ctx context.Context, e endpoint, args []string, query url.Values, out any) error {
	path := e.Path(args...)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return c.do(ctx, e.Method, path, out)
}

// post issues a write and decodes the answer.
//
// PVE takes form-encoded parameters, not JSON: that is what its schema layer
// parses, and sending JSON gets a 400 that blames the parameters.
func (c *Client) post(ctx context.Context, e endpoint, args []string, body url.Values, out any) error {
	return c.write(ctx, e.Method, e.Path(args...), body, out)
}

// del issues a DELETE, whose parameters travel in the QUERY STRING.
//
// A DELETE carrying a form body earns `501 — Unexpected content for method
// 'DELETE'` from PVE's own HTTP server, before the schema layer is ever
// reached. It is worth its own helper because that 501 is indistinguishable
// from the one a wrong path produces: the message points at the method, the
// cause is the body, and `pvecli vm rm --purge` failed that way until PVX-031
// exercised it against the lab.
func (c *Client) del(ctx context.Context, e endpoint, args []string, query url.Values, out any) error {
	path := e.Path(args...)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return c.do(ctx, e.Method, path, out)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	return c.write(ctx, method, path, nil, out)
}

// getRaw hands back the WHOLE envelope instead of just `data`.
//
// Every PVE answer wraps its payload in {"data": …} — except when the handler
// calls set_result_attrib, which drops extra keys as SIBLINGS of `data`.
// GET /nodes/{node}/network is the one endpoint this CLI calls that does it:
// the pending network diff arrives in a top-level "changes", and unwrapping
// `data` throws it away. Reading it is the whole point of PVX-049.
func (c *Client) getRaw(ctx context.Context, e endpoint, args []string, query url.Values) ([]byte, error) {
	path := e.Path(args...)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return c.exchange(ctx, e.Method, path, nil)
}

func (c *Client) write(ctx context.Context, method, path string, body url.Values, out any) error {
	raw, err := c.exchange(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}

	// Every PVE answer wraps its payload in {"data": …}. Unwrapping here means
	// no caller ever has to know that; the shape of `data` stays theirs to
	// decode, because it is a list here, an object there, and a bare string
	// (a UPID) for every mutation.
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s %s : réponse JSON illisible : %w", method, path, err)
	}
	// A null payload is a legitimate answer, not a failure: PVE replies
	// {"data": null} to a synchronous mutation — a config change on a stopped
	// guest, for instance. Treating that as an error made a successful write
	// look broken.
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("%s %s : décodage de « data » : %w", method, path, err)
	}
	return nil
}

// MultipartField is one ordered form field of an upload.
//
// Ordered, and that word is load-bearing. PVE does not parse the multipart
// body with a general-purpose parser: PVE::APIServer::AnyEvent walks it as a
// state machine, extracting `content`, then `checksum-algorithm`, then
// `checksum`, each with a regexp ANCHORED at the start of the remaining
// buffer, and only then the file part. Send the fields in any other order and
// they are silently dropped — the upload then fails on a missing parameter
// that the request visibly contained.
type MultipartField struct{ Name, Value string }

// postMultipart uploads a file. It is the only request in this CLI that is not
// form-encoded, and the only one whose body is streamed rather than built in
// memory: an ISO does not belong in a []byte.
//
// The file part MUST be named "filename" — the node dies with « wrong field
// name … expected 'filename' » otherwise — and its filename attribute is what
// the volume will be called on the storage.
//
// The body is assembled as head + file + tail rather than written through an
// io.Pipe, because the SIZE has to be known before the first byte goes out.
// Handing net/http a body of unknown length makes it fall back to chunked
// transfer encoding, and PVE's own HTTP server answers that with
// « 501 — chunked transfer encoding not supported ». Streaming is preserved:
// only the multipart preamble is buffered, never the file.
func (c *Client) postMultipart(
	ctx context.Context,
	e endpoint,
	args []string,
	fields []MultipartField,
	name string,
	size int64,
	body io.Reader,
	out any,
) error {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	for _, f := range fields {
		if err := mw.WriteField(f.Name, f.Value); err != nil {
			return err
		}
	}
	if _, err := mw.CreateFormFile("filename", name); err != nil {
		return err
	}
	// What Close() would write, kept apart so the file can sit between the two.
	tail := "\r\n--" + mw.Boundary() + "--\r\n"

	path := e.Path(args...)
	u := *c.base
	u.Path += path

	req, err := http.NewRequestWithContext(ctx, e.Method, u.String(),
		io.MultiReader(&head, body, strings.NewReader(tail)))
	if err != nil {
		return err
	}
	req.ContentLength = int64(head.Len()) + size + int64(len(tail))
	c.applyAuth(req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.setAccessHeaders(req)

	if c.trace != nil {
		safe := redactedHeader(req, c.tokenID)
		// The body is a file: tracing it would dump an ISO onto the terminal.
		c.trace.Request(req.Method, req.URL.String(), safe, []byte("<corps multipart, non tracé>"))
	}

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return c.transportError(e.Method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("%s %s : lecture de la réponse : %w", e.Method, path, err)
	}
	if c.trace != nil {
		c.trace.Response(resp.StatusCode, time.Since(started), resp.Header, raw)
	}
	if resp.StatusCode >= 400 {
		return &APIError{
			Status:  resp.StatusCode,
			Method:  e.Method,
			Path:    path,
			Message: serverMessage(raw),
			Raw:     string(raw),
		}
	}
	if out == nil {
		return nil
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s %s : réponse JSON illisible : %w", e.Method, path, err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

// exchange performs one request and returns the raw body of a successful
// answer. It is the only place that speaks HTTP.
func (c *Client) exchange(ctx context.Context, method, path string, body url.Values) ([]byte, error) {
	// url.URL keeps path and query in separate fields, and String() escapes
	// whatever is in Path. Appending "?limit=5" to Path therefore produces a
	// request for a path that literally contains a question mark — which PVE
	// answers 501, "method not implemented", for reasons that read like a
	// missing endpoint rather than a malformed URL.
	u := *c.base
	rawPath, rawQuery, _ := strings.Cut(path, "?")
	u.Path += rawPath
	u.RawQuery = rawQuery

	var payload io.Reader
	if body != nil {
		payload = strings.NewReader(body.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), payload)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.setAccessHeaders(req)

	c.traceRequest(req, body)

	started := time.Now()
	resp, err := c.http.Do(req)
	took := time.Since(started)
	if err != nil {
		return nil, c.transportError(method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s %s : lecture de la réponse : %w", method, path, err)
	}
	if c.trace != nil {
		c.trace.Response(resp.StatusCode, took, resp.Header, raw)
	}

	if resp.StatusCode >= 400 {
		return nil, &APIError{
			Status:  resp.StatusCode,
			Method:  method,
			Path:    path,
			Message: serverMessage(raw),
			Raw:     string(raw),
		}
	}

	return raw, nil
}

// traceRequest hands the exchange to the tracer with the Authorization header
// already replaced. The secret does not leave this package: redaction in
// internal/log is a second line of defence, not the only one.
// Header names of a Cloudflare Access service token. They are what lets a
// non-browser client through an Access application: without them, Access
// answers a redirect to its login page, and the CLI reports HTML where it
// expected JSON.
const (
	accessIDHeader     = "CF-Access-Client-Id"
	accessSecretHeader = "CF-Access-Client-Secret"
)

// setAccessHeaders presents the service token, when one is configured. Against
// a node reached directly on the LAN there is none, and nothing is added.
func (c *Client) setAccessHeaders(req *http.Request) {
	if c.accessID == "" || c.accessSecret == "" {
		return
	}
	req.Header.Set(accessIDHeader, c.accessID)
	req.Header.Set(accessSecretHeader, c.accessSecret)
}

// redactedHeader is the only header map that ever leaves this package. Every
// credential it can carry is masked here, in one place: a secret added to a
// request and forgotten here would surface in the first --trace run.
func redactedHeader(req *http.Request, tokenID string) http.Header {
	safe := req.Header.Clone()
	if safe.Get("Authorization") != "" {
		safe.Set("Authorization", "PVEAPIToken="+tokenID+"=<redacted>")
	}
	if safe.Get(accessSecretHeader) != "" {
		safe.Set(accessSecretHeader, "<redacted>")
	}
	// Le ticket voyage dans un cookie et non dans Authorization : sans ces deux
	// lignes, un « pvecli login -vv » écrirait sur stderr une session root
	// valide deux heures. Un identifiant reste un identifiant, quel que soit
	// l'en-tête qui le porte.
	if safe.Get("Cookie") != "" {
		safe.Set("Cookie", "PVEAuthCookie=<redacted>")
	}
	if safe.Get("CSRFPreventionToken") != "" {
		safe.Set("CSRFPreventionToken", "<redacted>")
	}
	return safe
}

func (c *Client) traceRequest(req *http.Request, body url.Values) {
	if c.trace == nil {
		return
	}
	safe := redactedHeader(req, c.tokenID)
	var raw []byte
	if body != nil {
		raw = []byte(body.Encode())
	}
	c.trace.Request(req.Method, req.URL.String(), safe, raw)
}

// transportError keeps a trust failure legible instead of letting it surface as
// a wall of x509 wrapping. A first contact with a self-signed lab node is the
// most likely failure of the whole CLI: it deserves the answer, not the stack.
func (c *Client) transportError(method, path string, err error) error {
	var pinned *CertError
	if errors.As(err, &pinned) {
		return pinned
	}

	var verifyErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if errors.As(err, &verifyErr) || errors.As(err, &unknownCA) || errors.As(err, &hostErr) {
		return &CertError{Host: c.base.Host, Reason: CertUnknown, Err: err}
	}

	return fmt.Errorf("%s %s : %w", method, path, err)
}

// serverMessage extracts what the node said about a failure. PVE answers an
// error either as plain text or as {"errors": {...}}; both are worth keeping.
func serverMessage(raw []byte) string {
	var body struct {
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(raw, &body); err == nil {
		// The `errors` map first: PVE's `message` is often the generic
		// "Parameter verification failed.", while `errors` names the parameter
		// that failed and why. Preferring the generic line hides the answer.
		if len(body.Errors) > 0 {
			parts := make([]string, 0, len(body.Errors))
			for k, v := range body.Errors {
				parts = append(parts, k+": "+v)
			}
			sort.Strings(parts)
			return strings.Join(parts, ", ")
		}
		if body.Message != "" {
			return strings.TrimSpace(body.Message)
		}
	}
	return strings.TrimSpace(string(raw))
}
