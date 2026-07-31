// Package catalog holds the list of services pvecli knows how to install in a
// declared VM, and the Terraform module and Ansible roles that install them.
//
// Nothing here talks to PVE, to Terraform or to Ansible. It parses a manifest
// and answers questions about it, so the resolution rules can be tested with no
// node powered on and no binary installed.
//
// The join between the three tools is a tag. A service `docker` becomes the PVE
// tag `svc_docker`, which `pvecli iac inventory` turns into the Ansible group
// `svc_docker`, which site.yml matches to run the `docker` role. That is the
// same mechanism the lab already used for `lab_apps`, generalised — which is
// why adding a service needs no new plumbing in the inventory.
package catalog

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// TagPrefix is what turns a service id into a PVE tag, and therefore into an
// Ansible group. PVE lowercases tags and rejects most punctuation, so the
// prefix is deliberately boring.
const TagPrefix = "svc_"

// ManifestName is the manifest inside Assets.
const ManifestName = "catalog.yaml"

//go:embed assets
var Assets embed.FS

// Output is one line of the connection block a service contributes once it is
// installed. Value is an Ansible expression the role resolves; pvecli never
// evaluates it itself.
type Output struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	// Secret outputs are stored in the OS keychain and never rendered. The
	// connection block shows the keychain reference instead.
	Secret bool `yaml:"secret"`
}

// Service is one entry of the catalogue.
type Service struct {
	ID       string   `yaml:"id"`
	Summary  string   `yaml:"summary"`
	Role     string   `yaml:"role"`
	Requires []string `yaml:"requires"`
	Ports    []int    `yaml:"ports"`
	Outputs  []Output `yaml:"outputs"`
}

// Tag is the PVE tag that carries this service onto a guest.
func (s Service) Tag() string { return TagPrefix + s.ID }

// Catalog is the parsed manifest.
type Catalog struct {
	Version  int       `yaml:"version"`
	Services []Service `yaml:"services"`
}

// UnknownServiceError names what was asked for and what exists. Listing the
// valid ids matters more than the refusal: an operator who mistypes `postgres`
// needs to be told the id is `postgresql`, not that they were wrong.
type UnknownServiceError struct {
	ID    string
	Known []string
}

func (e *UnknownServiceError) Error() string {
	return fmt.Sprintf("service inconnu « %s » — le catalogue propose : %s",
		e.ID, strings.Join(e.Known, ", "))
}

// Load parses the manifest embedded in the binary. It cannot describe a service
// this build does not ship, which is the point.
func Load() (*Catalog, error) {
	b, err := Assets.ReadFile("assets/" + ManifestName)
	if err != nil {
		return nil, fmt.Errorf("catalogue embarqué illisible: %w", err)
	}
	return Parse(b)
}

// Parse decodes and validates a manifest.
func Parse(b []byte) (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("catalogue illisible: %w", err)
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("version de catalogue non supportée: %d (attendu 1)", c.Version)
	}
	if len(c.Services) == 0 {
		return nil, fmt.Errorf("catalogue vide")
	}

	seen := make(map[string]bool, len(c.Services))
	for _, s := range c.Services {
		switch {
		case s.ID == "":
			return nil, fmt.Errorf("un service sans id")
		case s.Role == "":
			return nil, fmt.Errorf("service « %s » sans rôle Ansible", s.ID)
		case seen[s.ID]:
			return nil, fmt.Errorf("service « %s » déclaré deux fois", s.ID)
		}
		seen[s.ID] = true
	}
	// Dependencies are checked in a second pass so a service may depend on one
	// declared after it.
	for _, s := range c.Services {
		for _, dep := range s.Requires {
			if !seen[dep] {
				return nil, fmt.Errorf("service « %s » dépend de « %s », qui n'existe pas", s.ID, dep)
			}
		}
	}
	if err := c.checkCycles(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Get returns a service by id.
func (c *Catalog) Get(id string) (Service, bool) {
	for _, s := range c.Services {
		if s.ID == id {
			return s, true
		}
	}
	return Service{}, false
}

// IDs lists every service id, in manifest order.
func (c *Catalog) IDs() []string {
	ids := make([]string, 0, len(c.Services))
	for _, s := range c.Services {
		ids = append(ids, s.ID)
	}
	return ids
}

// Resolve expands a list of requested ids into the services to install,
// dependencies first and each one only once. The order is deterministic: a
// dependency always precedes what needs it, and ties are broken by manifest
// order, so two runs of the same request produce the same playbook.
func (c *Catalog) Resolve(ids []string) ([]Service, error) {
	var out []Service
	done := make(map[string]bool)

	var visit func(id string) error
	visit = func(id string) error {
		if done[id] {
			return nil
		}
		s, ok := c.Get(id)
		if !ok {
			return &UnknownServiceError{ID: id, Known: c.IDs()}
		}
		done[id] = true
		for _, dep := range s.Requires {
			if err := visit(dep); err != nil {
				return err
			}
		}
		out = append(out, s)
		return nil
	}

	for _, id := range ids {
		if err := visit(strings.TrimSpace(id)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Tags returns the PVE tags carrying a set of services, sorted so a declaration
// rewritten with the same services produces the same tag list -- otherwise
// every `iac plan` would report a spurious change.
func Tags(services []Service) []string {
	tags := make([]string, 0, len(services))
	for _, s := range services {
		tags = append(tags, s.Tag())
	}
	sort.Strings(tags)
	return tags
}

// checkCycles refuses a manifest whose dependencies loop. Resolve would
// otherwise mark a node done on the way in and silently return an order that
// installs a service before what it needs.
func (c *Catalog) checkCycles() error {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // fully explored
	)
	state := make(map[string]int, len(c.Services))

	var walk func(id string) error
	walk = func(id string) error {
		switch state[id] {
		case grey:
			return fmt.Errorf("dépendances circulaires autour de « %s »", id)
		case black:
			return nil
		}
		state[id] = grey
		s, _ := c.Get(id)
		for _, dep := range s.Requires {
			if err := walk(dep); err != nil {
				return err
			}
		}
		state[id] = black
		return nil
	}

	for _, s := range c.Services {
		if err := walk(s.ID); err != nil {
			return err
		}
	}
	return nil
}
