package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MakFly/pvecli/internal/iac"
	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/secret"
)

// connectHarness runs reportConnections over a directory of published outputs,
// with the keychain replaced so no test touches the real one.
func connectHarness(t *testing.T, available bool, files map[string]string, args ...string) (stdout, stderr string, stored map[string]string, err error) {
	t.Helper()

	dir := t.TempDir()
	for name, body := range files {
		if werr := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}

	stored = map[string]string{}
	oldAvail, oldStore := secretAvailable, secretStore
	secretAvailable = func() bool { return available }
	secretStore = func(ref secret.Ref, value string) error {
		stored[ref.String()] = value
		return nil
	}
	t.Cleanup(func() { secretAvailable, secretStore = oldAvail, oldStore })

	var out, errOut bytes.Buffer
	c := &cobra.Command{Use: "fake", RunE: func(cmd *cobra.Command, _ []string) error {
		inv := &iac.Inventory{Hosts: []iac.Host{{Name: "app-01", VMID: 220, IP: "192.0.2.220", User: "ops"}}}
		return reportConnections(cmd, inv, dir)
	}}
	addRenderFlags(c)
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)

	err = c.Execute()
	return out.String(), errOut.String(), stored, err
}

func TestConnectionBlockCarriesTheWayIn(t *testing.T) {
	stdout, _, _, err := connectHarness(t, true, map[string]string{
		"app-01.docker.json":     `{"docker":"27.3.1"}`,
		"app-01.postgresql.json": `{"postgresql.user":"app","postgresql.host":"192.0.2.220:5432"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ssh ops@192.0.2.220", // the answer to "and now, how do I get in?"
		"192.0.2.220:5432",
		"27.3.1",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("le bloc doit contenir %q :\n%s", want, stdout)
		}
	}
}

// The one that must never regress: a generated password must not reach stdout,
// which is what gets piped, redirected and pasted.
func TestASecretNeverReachesStdout(t *testing.T) {
	const password = "tr3s-s3cret-ne-doit-jamais-sortir"

	stdout, stderr, stored, err := connectHarness(t, true, map[string]string{
		"app-01.postgresql.json": `{"postgresql.user":"app","postgresql.password":"` + password + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, password) {
		t.Errorf("le mot de passe est sorti sur stdout :\n%s", stdout)
	}
	if strings.Contains(stderr, password) {
		t.Errorf("le trousseau était disponible : rien ne devait être affiché :\n%s", stderr)
	}
	if got := stored["pvecli-app-01 / postgresql.password"]; got != password {
		t.Errorf("trousseau = %v, le mot de passe devait y être rangé", stored)
	}
	if !strings.Contains(stdout, "find-generic-password") {
		t.Errorf("le bloc doit dire comment le relire :\n%s", stdout)
	}
}

// Without a keychain, the value is shown once on stderr rather than pretending
// it was stored somewhere.
func TestWithoutAKeychainTheSecretIsShownOnceOnStderr(t *testing.T) {
	const password = "valeur-affichee-une-fois"

	stdout, stderr, stored, err := connectHarness(t, false, map[string]string{
		"app-01.postgresql.json": `{"postgresql.password":"` + password + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Errorf("rien ne devait être rangé : %v", stored)
	}
	if !strings.Contains(stderr, password) {
		t.Errorf("la valeur doit être montrée une fois sur stderr :\n%s", stderr)
	}
	if strings.Contains(stdout, password) {
		t.Errorf("même sans trousseau, stdout reste propre :\n%s", stdout)
	}
}

// The block is a result, so it has to survive `-o json | jq` like every other.
func TestConnectionBlockSurvivesJSONOutput(t *testing.T) {
	stdout, _, _, err := connectHarness(t, true, map[string]string{
		"app-01.postgresql.json": `{"postgresql.user":"app","postgresql.password":"secret"}`,
	}, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}

	var conns []output.Connection
	if err := json.Unmarshal([]byte(stdout), &conns); err != nil {
		t.Fatalf("stdout n'est pas du JSON : %v\n%s", err, stdout)
	}
	if len(conns) != 1 || conns[0].Host != "app-01" || conns[0].IP != "192.0.2.220" {
		t.Fatalf("connexions = %+v", conns)
	}
	for _, e := range conns[0].Entries {
		if e.Key == "postgresql.password" {
			if !e.Secret {
				t.Error("l'entrée doit être marquée secret dans le JSON")
			}
			if strings.Contains(e.Value, "secret") && !strings.Contains(e.Value, "find-generic-password") {
				t.Errorf("la valeur du secret a fui dans le JSON : %q", e.Value)
			}
		}
	}
}

// Nothing published means nothing to say -- an empty block would suggest the
// run had done something it did not.
func TestNoOutputsMeansNoBlock(t *testing.T) {
	stdout, stderr, _, err := connectHarness(t, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout doit rester vide :\n%s", stdout)
	}
	if strings.Contains(stderr, "accès aux services") {
		t.Errorf("aucun en-tête ne doit être imprimé :\n%s", stderr)
	}
}
