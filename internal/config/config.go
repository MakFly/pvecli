// Package config resolves the effective configuration by layering, in
// decreasing priority: flags > environment > file > defaults (PRD §7.1).
//
// The token secret is deliberately never read from the config file: it comes
// from the environment, itself fed by the OS keychain (decision D1). Load and
// SetKey both refuse the key outright rather than tolerating it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment variable names. They match the `pve-api` bash client of the
// reference lab, so that both tools can share a shell session.
const (
	EnvEndpoint    = "PVE_API_URL"
	EnvTokenID     = "PVE_API_TOKEN_ID"
	EnvTokenSecret = "PVE_API_TOKEN_SECRET"
	EnvInsecure    = "PVE_INSECURE"
)

// Cloudflare's own conventional variable names, so a shell already set up for
// `wrangler` or `flarectl` needs nothing extra.
const (
	EnvCFToken   = "CF_API_TOKEN"
	EnvAccountID = "CF_ACCOUNT_ID"
)

// The service token of a Cloudflare Access application sitting in front of the
// node. Access turns away anything that is not a browser, so without these a
// remote `pvecli` gets a redirect to a login page instead of the API.
//
// Same rule as PVE_API_TOKEN_SECRET: the environment only. A secret in the
// config file outlives the session it was needed for.
const (
	EnvAccessClientID     = "CF_ACCESS_CLIENT_ID"
	EnvAccessClientSecret = "CF_ACCESS_CLIENT_SECRET"
)

// File is the on-disk document.
type File struct {
	CurrentContext string              `yaml:"current_context"`
	Contexts       map[string]*Context `yaml:"contexts"`
}

// Context is one target: which node, and how to reach it.
type Context struct {
	Endpoint string `yaml:"endpoint,omitempty"`
	TokenID  string `yaml:"token_id,omitempty"`
	Node     string `yaml:"node,omitempty"`
	Insecure bool   `yaml:"insecure,omitempty"`
	TLS      TLS    `yaml:"tls,omitempty"`
	IaC      IaC    `yaml:"iac,omitempty"`
	CF       CF     `yaml:"cf,omitempty"`

	// DetectedVersion is written by `pvecli version`, not by a human. It is
	// what later stories consult to decide whether an endpoint exists in this
	// PVE release — an endpoint "that does not exist" is usually an endpoint
	// from another version.
	DetectedVersion string `yaml:"detected_version,omitempty"`
}

// TLS carries how the server certificate is verified. Filled by PVX-004
// (`config trust`), read by the HTTP client.
type TLS struct {
	Fingerprint string `yaml:"fingerprint,omitempty"`
	CAFile      string `yaml:"ca_file,omitempty"`
}

// DefaultManagedTag is the tag the lab repository puts on everything Terraform
// owns (`tags = ["lab","terraform","managed"]`). It is a default, not a
// constant of the domain: another repository may have picked another word, and
// `iac.managed_tag` is where it says so.
const DefaultManagedTag = "managed"

// IaC locates the infrastructure-as-code repository this node is driven from.
//
// The two directories are paths on the operator's machine, not on the node:
// `pvecli` runs terraform and ansible locally and talks to PVE over the API.
// Storing them in the config is what lets `pvecli iac …` be typed from
// anywhere instead of only from inside the repository.
type IaC struct {
	TerraformDir string `yaml:"terraform_dir,omitempty"`
	AnsibleDir   string `yaml:"ansible_dir,omitempty"`

	// ManagedTag overrides DefaultManagedTag for the ownership guard.
	ManagedTag string `yaml:"managed_tag,omitempty"`
}

// CF locates the Cloudflare account the tunnels are created in.
//
// Only the account id lives here. The API token is a secret, and follows the
// same rule as the PVE token (decision D1): environment only, fed from the
// keychain, never stored in a file this tool writes.
type CF struct {
	AccountID string `yaml:"account_id,omitempty"`
}

// WritableKeys is the exhaustive set of keys `config set` accepts. An unknown
// key is a typo, not a new setting — the CLI says so instead of silently
// storing something nothing will ever read.
var WritableKeys = []string{
	"endpoint",
	"token_id",
	"node",
	"insecure",
	"tls.fingerprint",
	"tls.ca_file",
	"iac.terraform_dir",
	"iac.ansible_dir",
	"iac.managed_tag",
	"cf.account_id",
}

// SetKey writes one dotted key into a context.
func SetKey(c *Context, key, value string) error {
	switch key {
	case "endpoint":
		c.Endpoint = value
	case "token_id":
		c.TokenID = value
	case "node":
		c.Node = value
	case "insecure":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("« insecure » attend un booléen, pas %q", value)
		}
		c.Insecure = b
	case "tls.fingerprint":
		c.TLS.Fingerprint = value
	case "tls.ca_file":
		c.TLS.CAFile = value

	// Absolutised on the way in. A relative path in a config file resolves
	// against whatever directory the operator happened to be in, so the same
	// stored value would point somewhere different at every invocation — the
	// kind of bug that looks like "terraform lost my state".
	case "iac.terraform_dir":
		p, err := absPath(value)
		if err != nil {
			return err
		}
		c.IaC.TerraformDir = p
	case "iac.ansible_dir":
		p, err := absPath(value)
		if err != nil {
			return err
		}
		c.IaC.AnsibleDir = p
	case "iac.managed_tag":
		c.IaC.ManagedTag = value

	// The account id, and only the account id. The Cloudflare API token is a
	// secret and follows the same rule as the PVE one: environment only, fed
	// from the keychain, never written to a file this tool manages.
	case "cf.account_id":
		c.CF.AccountID = value

	// Settable but not advertised in WritableKeys: `pvecli version` writes it,
	// a human has no reason to.
	case "detected_version":
		c.DetectedVersion = value

	case "token_secret":
		return refuseTokenSecret("« token_secret » ne peut pas être écrit dans le fichier de configuration.")

	default:
		return fmt.Errorf("clé inconnue %q ; clés acceptées : %s", key, strings.Join(WritableKeys, ", "))
	}
	return nil
}

// absPath turns a path typed on the command line into an absolute one,
// expanding a leading ~ itself: the shell does that expansion for a bare `~`,
// but not inside a quoted argument, and the config file is the wrong place to
// discover the difference.
func absPath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("impossible de déterminer le dossier personnel : %w", err)
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~"))
	}
	return filepath.Abs(value)
}

// refuseTokenSecret builds the one message the CLI gives whenever the secret
// is found in, or headed for, the config file. Reading and writing share it:
// the reasoning is the same in both directions.
func refuseTokenSecret(what string) error {
	return fmt.Errorf(`%s

Un secret ne doit jamais vivre dans un fichier de configuration : le fichier
finit committé, sauvegardé, ou lu par un autre processus.

Passe par l'environnement :
  export %s="…"`, what, EnvTokenSecret)
}
