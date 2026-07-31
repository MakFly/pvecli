package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/MakFly/pvectl/internal/pve"
	"github.com/MakFly/pvectl/internal/testutil"
)

// `--can` exists to be used in a shell `if`. Its contract is therefore an exit
// code, and a stdout carrying one word and nothing else.
func TestCanAnswersOnStdoutAndExitCode(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/access/permissions": "permissions.json",
	})
	point(t, srv.URL)

	stdout, _, err := run(t, "access", "whoami", "--path", "/vms", "--can", "VM.PowerMgmt")
	if err != nil {
		t.Fatalf("un privilège détenu doit sortir en 0 : %v", err)
	}
	if strings.TrimSpace(stdout) != "oui" {
		t.Errorf("stdout = %q, want \"oui\"", stdout)
	}

	stdout, _, err = run(t, "access", "whoami", "--path", "/vms", "--can", "Permissions.Modify")
	if strings.TrimSpace(stdout) != "non" {
		t.Errorf("stdout = %q, want \"non\"", stdout)
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 1 {
		t.Errorf("un privilège absent doit sortir en 1, got %v", err)
	}
}

// --can without --path cannot be answered: a privilege is held ON a path.
// Refusing is more useful than answering "non" about nothing.
func TestCanRequiresAPath(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/access/permissions": "permissions.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "whoami", "--can", "VM.PowerMgmt")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("--can sans --path est une erreur d'usage, got %v", err)
	}
}

// Administrator on "/" is root@pam under another name. The guard is the whole
// point of PVX-035: the CLI has to make the good practice easier than the bad
// one, and this is the bad one.
func TestAdministratorOnRootIsRefused(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/access/acl": "acl.json",
		"PUT /api2/json/access/acl": "upid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "acl", "set", "--path", "/", "--role", "Administrator",
		"--user", "automation@pve", "--yes")
	if err == nil {
		t.Fatal("Administrator sur / doit être refusé sans le drapeau")
	}
	if !strings.Contains(err.Error(), "i-know-what-im-doing") {
		t.Errorf("le refus doit nommer la porte de sortie : %v", err)
	}
	for _, req := range srv.Requests {
		if strings.HasPrefix(req, "PUT ") {
			t.Fatalf("le refus doit précéder toute écriture : %v", srv.Requests)
		}
	}

	// Narrower targets go through: the guard is about "/", not about the role
	// existing at all.
	if _, _, err := run(t, "access", "acl", "set", "--path", "/vms/120", "--role", "PVEVMAdmin",
		"--user", "automation@pve", "--dry-run"); err != nil {
		t.Errorf("un rôle ciblé sur un chemin précis ne doit pas être bloqué : %v", err)
	}
}

// A token that never expires has to be a decision, not an oversight.
func TestTokenCreateDemandsAnExpiry(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "access", "token", "create", "automation@pve", "terraform")
	if err == nil || !strings.Contains(err.Error(), "--no-expire") {
		t.Errorf("--expire doit être exigé, avec sa porte de sortie : %v", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir : %v", srv.Requests)
	}
}

// The plan of a token creation is printed on stderr. It must not carry the
// secret — and neither must a --verbose trace of the same command.
func TestTokenPlanNeverPrintsASecret(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, stderr, _ := run(t, "-vv", "access", "token", "create", "automation@pve", "terraform",
		"--expire", "2026-12-31", "--dry-run")

	if strings.Contains(stderr, "s3cr3t") {
		t.Errorf("le secret du token courant a fuité dans le plan ou la trace :\n%s", stderr)
	}
}
