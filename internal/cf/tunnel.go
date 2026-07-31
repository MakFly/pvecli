package cf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// CatchAllService is the rule cloudflared requires at the end of every ingress
// table. Without it the connector refuses to start — not at configuration time,
// but the next time it is restarted, which is the worst moment to find out.
const CatchAllService = "http_status:404"

// TunnelDomain is what a hostname CNAMEs to in order to enter a tunnel.
const TunnelDomain = "cfargotunnel.com"

// Tunnel is one connector group.
type Tunnel struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	DeletedAt *string `json:"deleted_at"`
}

// Deleted reports whether Cloudflare still lists a tunnel it considers gone.
// The API returns them, and acting on one produces errors that blame the wrong
// thing.
func (t Tunnel) Deleted() bool { return t.DeletedAt != nil && *t.DeletedAt != "" }

// CNAME is the record content that routes a hostname into this tunnel.
func (t Tunnel) CNAME() string { return t.ID + "." + TunnelDomain }

// ErrNotFound is returned when a named tunnel, zone or record does not exist.
var ErrNotFound = errors.New("introuvable")

// Tunnels lists the account's tunnels, deleted ones excluded.
func (c *Client) Tunnels(ctx context.Context) ([]Tunnel, error) {
	var all []Tunnel
	if err := c.do(ctx, epTunnels, []string{c.account}, url.Values{"per_page": {"100"}}, nil, &all); err != nil {
		return nil, err
	}
	live := make([]Tunnel, 0, len(all))
	for _, t := range all {
		if !t.Deleted() {
			live = append(live, t)
		}
	}
	return live, nil
}

// TunnelByName resolves a name to a tunnel.
func (c *Client) TunnelByName(ctx context.Context, name string) (Tunnel, error) {
	tunnels, err := c.Tunnels(ctx)
	if err != nil {
		return Tunnel{}, err
	}
	var matches []Tunnel
	for _, t := range tunnels {
		if t.Name == name {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		names := make([]string, 0, len(tunnels))
		for _, t := range tunnels {
			names = append(names, t.Name)
		}
		if len(names) == 0 {
			return Tunnel{}, fmt.Errorf("aucun tunnel « %s » : %w — le compte n'en a aucun", name, ErrNotFound)
		}
		return Tunnel{}, fmt.Errorf("aucun tunnel « %s » : %w — le compte en a : %s",
			name, ErrNotFound, strings.Join(names, ", "))
	default:
		// Cloudflare allows duplicate names. Picking one would work until the
		// day it picks the other.
		return Tunnel{}, fmt.Errorf("%d tunnels portent le nom « %s » — désigne-le par son identifiant", len(matches), name)
	}
}

// CreateTunnel creates a remotely-managed tunnel, whose ingress table lives in
// the API rather than in a config.yml on the connector. That is what makes
// adding a route a one-line change with no file to edit inside the guest.
func (c *Client) CreateTunnel(ctx context.Context, name string) (Tunnel, error) {
	body := map[string]any{"name": name, "config_src": "cloudflare"}
	var t Tunnel
	if err := c.do(ctx, epTunnelCreate, []string{c.account}, nil, body, &t); err != nil {
		return Tunnel{}, err
	}
	return t, nil
}

// DeleteTunnel removes a tunnel. Cloudflare refuses while connectors are still
// attached, which is a useful refusal: it means something is still routing.
func (c *Client) DeleteTunnel(ctx context.Context, id string) error {
	return c.do(ctx, epTunnelDelete, []string{c.account, id}, nil, nil, nil)
}

// TunnelToken returns the connector token, the single value cloudflared needs
// inside the guest. It is a credential: it never goes into a table, a log or a
// command line.
func (c *Client) TunnelToken(ctx context.Context, id string) (string, error) {
	var token string
	if err := c.do(ctx, epTunnelToken, []string{c.account, id}, nil, nil, &token); err != nil {
		return "", err
	}
	return token, nil
}

// IngressRule is one line of the routing table.
type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Path     string `json:"path,omitempty"`
	Service  string `json:"service"`
}

// IsCatchAll reports whether this is the terminal rule.
func (r IngressRule) IsCatchAll() bool { return r.Hostname == "" && r.Path == "" }

// Config is a tunnel's routing table.
type Config struct {
	Ingress []IngressRule `json:"ingress"`
}

// TunnelConfig reads the current ingress table.
func (c *Client) TunnelConfig(ctx context.Context, id string) (Config, error) {
	var wrapper struct {
		Config Config `json:"config"`
	}
	if err := c.do(ctx, epTunnelConfig, []string{c.account, id}, nil, nil, &wrapper); err != nil {
		return Config{}, err
	}
	return wrapper.Config, nil
}

// SetTunnelConfig replaces the ingress table, after making sure it ends the way
// cloudflared requires.
func (c *Client) SetTunnelConfig(ctx context.Context, id string, cfg Config) error {
	cfg.Normalise()
	if err := cfg.Validate(); err != nil {
		return err
	}
	return c.do(ctx, epTunnelSetCfg, []string{c.account, id}, nil, map[string]any{"config": cfg}, nil)
}

// Normalise moves the catch-all to the end, keeping exactly one.
//
// The ingress table is ordered and evaluated top to bottom: a catch-all sitting
// anywhere but last swallows every rule below it. That failure is silent — the
// tunnel runs, the hostname resolves, and every request returns 404.
func (cfg *Config) Normalise() {
	kept := make([]IngressRule, 0, len(cfg.Ingress)+1)
	catchAll := IngressRule{Service: CatchAllService}
	found := false
	for _, r := range cfg.Ingress {
		if r.IsCatchAll() {
			if !found {
				// A deliberately customised catch-all is preserved.
				catchAll = r
				found = true
			}
			continue
		}
		kept = append(kept, r)
	}
	cfg.Ingress = append(kept, catchAll)
}

// Validate refuses a table cloudflared would reject.
func (cfg Config) Validate() error {
	if len(cfg.Ingress) == 0 {
		return errors.New("table d'ingress vide : cloudflared exige au moins la règle finale " + CatchAllService)
	}
	last := cfg.Ingress[len(cfg.Ingress)-1]
	if !last.IsCatchAll() {
		return fmt.Errorf("la dernière règle doit être un catch-all (%s) : cloudflared refuse de démarrer sans", CatchAllService)
	}
	for i, r := range cfg.Ingress[:len(cfg.Ingress)-1] {
		if r.IsCatchAll() {
			return fmt.Errorf("règle %d : un catch-all avant la fin avale toutes les règles suivantes", i+1)
		}
		if r.Service == "" {
			return fmt.Errorf("règle %d (%s) : aucun service de destination", i+1, r.Hostname)
		}
	}
	return nil
}

// AddRoute points a hostname at a local service, replacing any rule that
// already claimed that hostname.
func (cfg *Config) AddRoute(hostname, service string) {
	cfg.RemoveRoute(hostname)
	cfg.Ingress = append(cfg.Ingress, IngressRule{Hostname: hostname, Service: service})
	cfg.Normalise()
}

// RemoveRoute drops the rules for a hostname and says whether it found any.
func (cfg *Config) RemoveRoute(hostname string) bool {
	kept := make([]IngressRule, 0, len(cfg.Ingress))
	removed := false
	for _, r := range cfg.Ingress {
		if !r.IsCatchAll() && r.Hostname == hostname {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	cfg.Ingress = kept
	cfg.Normalise()
	return removed
}
