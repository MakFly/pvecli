package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{"docker", "postgresql", "cloudflared"} {
		if _, ok := c.Get(want); !ok {
			t.Errorf("le catalogue embarqué ne propose pas « %s »", want)
		}
	}
}

// Every role named by the manifest must exist in the embedded assets. A
// catalogue that advertises a service whose role was never shipped fails at
// `ansible-playbook` time, on the node, minutes into a run.
func TestEveryRoleIsShipped(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Services {
		path := "assets/ansible/roles/" + s.Role + "/tasks/main.yml"
		if _, err := Assets.ReadFile(path); err != nil {
			t.Errorf("service « %s » déclare le rôle « %s », mais %s est absent", s.ID, s.Role, path)
		}
	}
}

func TestResolveOrdersDependenciesFirst(t *testing.T) {
	c := mustParse(t, `
version: 1
services:
  - {id: caddy, role: caddy, requires: [docker]}
  - {id: docker, role: docker}
`)
	got, err := c.Resolve([]string{"caddy"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 2 || got[0].ID != "docker" || got[1].ID != "caddy" {
		t.Errorf("Resolve = %v, docker doit précéder caddy", ids(got))
	}
}

func TestResolveDeduplicates(t *testing.T) {
	c := mustParse(t, `
version: 1
services:
  - {id: docker, role: docker}
  - {id: caddy, role: caddy, requires: [docker]}
`)
	got, err := c.Resolve([]string{"docker", "caddy", "docker"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("Resolve = %v, un service demandé deux fois ne s'installe qu'une", ids(got))
	}
}

// The message is the feature: an operator who types `postgres` needs the real
// id, not a bare refusal.
func TestResolveUnknownServiceListsTheKnownOnes(t *testing.T) {
	c := mustParse(t, `
version: 1
services:
  - {id: postgresql, role: postgresql}
`)
	_, err := c.Resolve([]string{"postgres"})
	var unknown *UnknownServiceError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, attendu *UnknownServiceError", err)
	}
	if !strings.Contains(err.Error(), "postgresql") {
		t.Errorf("le message doit citer les ids valides: %q", err.Error())
	}
}

func TestParseRefusesUnknownDependency(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
services:
  - {id: caddy, role: caddy, requires: [docker]}
`))
	if err == nil || !strings.Contains(err.Error(), "docker") {
		t.Errorf("err = %v, une dépendance inexistante doit être refusée en citant son nom", err)
	}
}

func TestParseRefusesCycles(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
services:
  - {id: a, role: a, requires: [b]}
  - {id: b, role: b, requires: [a]}
`))
	if err == nil || !strings.Contains(err.Error(), "circulaires") {
		t.Errorf("err = %v, un cycle doit être refusé", err)
	}
}

func TestParseRefusesDuplicateID(t *testing.T) {
	_, err := Parse([]byte(`
version: 1
services:
  - {id: docker, role: docker}
  - {id: docker, role: autre}
`))
	if err == nil || !strings.Contains(err.Error(), "deux fois") {
		t.Errorf("err = %v, un id dupliqué doit être refusé", err)
	}
}

func TestParseRefusesUnsupportedVersion(t *testing.T) {
	if _, err := Parse([]byte("version: 2\nservices: [{id: a, role: a}]")); err == nil {
		t.Error("une version de catalogue inconnue doit être refusée")
	}
}

// Tags must be stable: an unsorted list would make `iac plan` report a change
// every time the same declaration is rewritten.
func TestTagsAreSorted(t *testing.T) {
	got := Tags([]Service{{ID: "postgresql"}, {ID: "docker"}})
	if len(got) != 2 || got[0] != "svc_docker" || got[1] != "svc_postgresql" {
		t.Errorf("Tags = %v, attendu trié", got)
	}
}

func mustParse(t *testing.T, doc string) *Catalog {
	t.Helper()
	c, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func ids(services []Service) []string {
	out := make([]string, len(services))
	for i, s := range services {
		out[i] = s.ID
	}
	return out
}
