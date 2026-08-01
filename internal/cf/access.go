package cf

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Cloudflare Access is the door in front of a tunnel. The tunnel makes a service
// reachable; Access decides who reaches it. Publishing a hostname without an
// Access application in front of it puts the origin on the open internet — which
// is why `cf route add` asks this package whether the name is covered.
//
// Two objects matter here:
//
//   - an APPLICATION is a hostname Access protects.
//   - a POLICY is a rule attached to it: a decision, and who it applies to.
//
// A policy with no application protects nothing; an application with no policy
// refuses everyone.

// Access decisions, as the API spells them.
const (
	// DecisionAllow lets an identity through after it authenticates.
	DecisionAllow = "allow"
	// DecisionServiceAuth is what a service token needs. It is NOT "allow":
	// an allow policy containing a service token would let the request through
	// without any authentication at all — the opposite of the intent.
	DecisionServiceAuth = "non_identity"
)

// AppTypeSelfHosted is the application type for a service behind a tunnel.
const AppTypeSelfHosted = "self_hosted"

// App is one Access application.
type App struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Domain          string `json:"domain"`
	Type            string `json:"type,omitempty"`
	SessionDuration string `json:"session_duration,omitempty"`
}

// Apps lists the account's Access applications.
func (c *Client) Apps(ctx context.Context) ([]App, error) {
	var out []App
	if err := c.do(ctx, epAccessApps, []string{c.account}, url.Values{"per_page": {"100"}}, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AppByDomain resolves an application by the hostname it protects.
//
// The domain is the identity of an application for an operator: two apps can
// share a name, never a domain.
func (c *Client) AppByDomain(ctx context.Context, domain string) (App, error) {
	apps, err := c.Apps(ctx)
	if err != nil {
		return App{}, err
	}
	for _, a := range apps {
		// Cloudflare stores the domain with its optional path; a bare hostname
		// matches the application that covers it.
		if a.Domain == domain || strings.HasPrefix(a.Domain, domain+"/") {
			return a, nil
		}
	}
	return App{}, fmt.Errorf("aucune application Access pour « %s » : %w", domain, ErrNotFound)
}

// CreateApp puts an Access application in front of a hostname.
func (c *Client) CreateApp(ctx context.Context, a App) (App, error) {
	a.Type = AppTypeSelfHosted
	var out App
	if err := c.do(ctx, epAccessAppCreate, []string{c.account}, nil, a, &out); err != nil {
		return App{}, err
	}
	return out, nil
}

// DeleteApp removes an application — and with it, the protection of its
// hostname. The tunnel keeps routing.
func (c *Client) DeleteApp(ctx context.Context, appID string) error {
	return c.do(ctx, epAccessAppDelete, []string{c.account, appID}, nil, nil, nil)
}

// Rule is one entry of a policy's include list. Exactly one field is set.
type Rule struct {
	Email        *EmailRule        `json:"email,omitempty"`
	EmailDomain  *EmailDomainRule  `json:"email_domain,omitempty"`
	ServiceToken *ServiceTokenRule `json:"service_token,omitempty"`
}

type EmailRule struct {
	Email string `json:"email"`
}

type EmailDomainRule struct {
	Domain string `json:"domain"`
}

type ServiceTokenRule struct {
	TokenID string `json:"token_id"`
}

// IncludeEmail matches one person by address.
func IncludeEmail(address string) Rule { return Rule{Email: &EmailRule{Email: address}} }

// IncludeEmailDomain matches everyone at a domain.
func IncludeEmailDomain(domain string) Rule {
	return Rule{EmailDomain: &EmailDomainRule{Domain: domain}}
}

// IncludeServiceToken matches one service token — a CLI, not a person.
func IncludeServiceToken(id string) Rule {
	return Rule{ServiceToken: &ServiceTokenRule{TokenID: id}}
}

// Policy is one rule attached to an application.
type Policy struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Decision string `json:"decision"`
	Include  []Rule `json:"include"`
}

// Describe renders who a policy lets in, for a table an operator reads before
// trusting it. A policy printed as an opaque id is a policy nobody checks.
func (p Policy) Describe() string {
	parts := make([]string, 0, len(p.Include))
	for _, r := range p.Include {
		switch {
		case r.Email != nil:
			parts = append(parts, r.Email.Email)
		case r.EmailDomain != nil:
			parts = append(parts, "@"+r.EmailDomain.Domain)
		case r.ServiceToken != nil:
			parts = append(parts, "service token "+r.ServiceToken.TokenID)
		}
	}
	if len(parts) == 0 {
		return "(personne)"
	}
	return strings.Join(parts, ", ")
}

// Validate refuses the policy that looks right and protects nothing.
func (p Policy) Validate() error {
	if len(p.Include) == 0 {
		return fmt.Errorf("une policy sans include n'admet personne — et ne protège donc rien")
	}
	hasToken := false
	for _, r := range p.Include {
		if r.ServiceToken != nil {
			hasToken = true
		}
	}
	// This is the mistake worth encoding. A service token inside an `allow`
	// policy is accepted by the API, and lets every request through with no
	// authentication whatsoever: `allow` means "an identity authenticated",
	// and a service token carries no identity.
	if hasToken && p.Decision == DecisionAllow {
		return fmt.Errorf("un service token dans une policy « %s » laisserait passer sans authentification — "+
			"utilise la décision « %s »", DecisionAllow, DecisionServiceAuth)
	}
	return nil
}

// Policies lists the rules attached to an application.
func (c *Client) Policies(ctx context.Context, appID string) ([]Policy, error) {
	var out []Policy
	if err := c.do(ctx, epAccessPolicies, []string{c.account, appID}, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddPolicy attaches a rule to an application.
func (c *Client) AddPolicy(ctx context.Context, appID string, p Policy) (Policy, error) {
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	var out Policy
	if err := c.do(ctx, epAccessPolicyAdd, []string{c.account, appID}, nil, p, &out); err != nil {
		return Policy{}, err
	}
	return out, nil
}

// DeletePolicy detaches a rule.
func (c *Client) DeletePolicy(ctx context.Context, appID, policyID string) error {
	return c.do(ctx, epAccessPolicyDel, []string{c.account, appID, policyID}, nil, nil, nil)
}

// ServiceToken is a credential for a client that is not a browser.
type ServiceToken struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	// ClientSecret comes back ONLY from the creation call. No later read
	// returns it: Cloudflare does not keep it in a readable form.
	ClientSecret string `json:"client_secret,omitempty"`
}

// ServiceTokens lists the account's service tokens, secrets excluded.
func (c *Client) ServiceTokens(ctx context.Context) ([]ServiceToken, error) {
	var out []ServiceToken
	if err := c.do(ctx, epAccessTokens, []string{c.account}, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateServiceToken mints a credential for a CLI.
func (c *Client) CreateServiceToken(ctx context.Context, name string) (ServiceToken, error) {
	var out ServiceToken
	body := map[string]string{"name": name}
	if err := c.do(ctx, epAccessTokenAdd, []string{c.account}, nil, body, &out); err != nil {
		return ServiceToken{}, err
	}
	return out, nil
}

// ServiceTokenByName resolves a token by the name it was created under.
func (c *Client) ServiceTokenByName(ctx context.Context, name string) (ServiceToken, error) {
	tokens, err := c.ServiceTokens(ctx)
	if err != nil {
		return ServiceToken{}, err
	}
	for _, t := range tokens {
		if t.Name == name {
			return t, nil
		}
	}
	return ServiceToken{}, fmt.Errorf("aucun service token « %s » : %w", name, ErrNotFound)
}
