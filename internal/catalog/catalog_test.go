package catalog

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestLoadEmbedded(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{"docker", "postgresql", "cloudflared", "caddy"} {
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

// ansiblePlay is the shape of one entry of pvecli.yml, just enough to check
// the join with the catalogue.
type ansiblePlay struct {
	Name  string   `yaml:"name"`
	Hosts string   `yaml:"hosts"`
	Roles []string `yaml:"roles"`
}

// A tag posted on a guest with no play behind it is metadata that lies: the
// service looks installable but nothing in pvecli.yml ever runs its role.
// This is the test that would have caught the debt PVX-078 documents.
func TestEveryServiceHasAPlay(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Assets.ReadFile("assets/ansible/pvecli.yml")
	if err != nil {
		t.Fatalf("lecture de pvecli.yml: %v", err)
	}
	var plays []ansiblePlay
	if err := yaml.Unmarshal(raw, &plays); err != nil {
		t.Fatalf("pvecli.yml illisible: %v", err)
	}

	for _, s := range c.Services {
		var found *ansiblePlay
		for i := range plays {
			if plays[i].Hosts == s.Tag() {
				found = &plays[i]
				break
			}
		}
		if found == nil {
			t.Errorf("service « %s » (tag %s) : aucun play de pvecli.yml ne vise ce groupe", s.ID, s.Tag())
			continue
		}
		roleFound := false
		for _, r := range found.Roles {
			if r == s.Role {
				roleFound = true
				break
			}
		}
		if !roleFound {
			t.Errorf("play « %s » (hosts: %s) ne joue pas le rôle « %s »", found.Name, found.Hosts, s.Role)
		}
	}
}

// `embed` ships whatever is on disk ; une faute de frappe dans `src:` ne se
// voit qu'au moment où ansible-playbook la cherche sur le nœud, minutes dans
// un run.
var srcTemplateRe = regexp.MustCompile(`src:\s*(\S+\.j2)`)

func TestEveryReferencedTemplateIsShipped(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range c.Services {
		if seen[s.Role] {
			continue
		}
		seen[s.Role] = true

		tasksPath := "assets/ansible/roles/" + s.Role + "/tasks/main.yml"
		raw, err := Assets.ReadFile(tasksPath)
		if err != nil {
			t.Fatalf("lecture de %s: %v", tasksPath, err)
		}
		for _, m := range srcTemplateRe.FindAllStringSubmatch(string(raw), -1) {
			tplPath := "assets/ansible/roles/" + s.Role + "/templates/" + m[1]
			if _, err := Assets.ReadFile(tplPath); err != nil {
				t.Errorf("rôle « %s » référence %s dans %s, mais le fichier est absent", s.Role, m[1], tasksPath)
			}
		}
	}
}

// Le manifeste dit ce qu'un service rend joignable ; le rôle doit réellement
// l'écrire. On ne compare que les clés : les valeurs diffèrent légitimement
// (ex. postgresql publie {{ ansible_host }} là où le manifeste affiche
// {{ pvecli_host_ip }}), seule la présence de la clé est un contrat vérifiable.
func TestEveryDeclaredOutputIsPublishedByItsRole(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Services {
		tasksPath := "assets/ansible/roles/" + s.Role + "/tasks/main.yml"
		raw, err := Assets.ReadFile(tasksPath)
		if err != nil {
			t.Fatalf("lecture de %s: %v", tasksPath, err)
		}
		body := string(raw)
		for _, o := range s.Outputs {
			if !strings.Contains(body, o.Key+":") {
				t.Errorf("service « %s » déclare la sortie « %s », absente de %s", s.ID, o.Key, tasksPath)
			}
		}
	}
}

// Contrairement à TestTagsAreSorted (qui appelle Tags sur un tableau littéral
// et ne peut donc jamais casser), celui-ci porte sur le catalogue embarqué :
// il garde un sens quand un service est ajouté.
func TestEmbeddedTagsAreSorted(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := Tags(c.Services)
	if !sort.StringsAreSorted(got) {
		t.Errorf("Tags(catalogue embarqué) = %v, non trié", got)
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
