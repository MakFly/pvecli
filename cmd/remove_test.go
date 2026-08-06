package cmd

import (
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/config"
	"github.com/dev-toolings/pvecli/internal/testutil"
)

// point aims the CLI at a replay server, so a whole command can be exercised
// with no Proxmox node powered on (PRD §9).
func point(t *testing.T, url string) {
	t.Helper()
	t.Setenv(config.EnvEndpoint, url)
	t.Setenv(config.EnvTokenID, "automation@pve!pvectl")
	t.Setenv(config.EnvTokenSecret, "s3cr3t")
}

// The ownership guard of PVX-031. What matters is not that the command fails —
// it is WHERE it fails: in the pre-read, before a single write leaves the
// process. A guard that refuses after the DELETE would be decoration.
func TestManagedGuardRefusesBeforeAnyWrite(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/lxc/121/status/current": "lxc-status-managed.json",
		"DELETE /api2/json/nodes/pve/lxc/121":             "upid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "lxc", "rm", "121", "--node", "pve", "--yes")
	if err == nil {
		t.Fatal("un guest tagué « managed » ne doit pas être détruit ici")
	}
	if !strings.Contains(err.Error(), "terraform destroy") {
		t.Errorf("le refus doit renvoyer vers le propriétaire : %v", err)
	}

	for _, req := range srv.Requests {
		if strings.HasPrefix(req, "DELETE ") {
			t.Fatalf("une écriture a été émise malgré la garde : %v", srv.Requests)
		}
	}
}

// The escape hatch has to exist and has to be the only way through: an operator
// who cannot override a guard eventually works around the CLI instead.
//
// The command still ends in an error here — the replay server keeps answering
// the post-read, so the deletion cannot be proven. That is the correct outcome
// for this fixture; what this test asserts is that the DELETE was attempted.
func TestForceUnmanagedLiftsTheGuard(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/lxc/121/status/current": "lxc-status-managed.json",
		"DELETE /api2/json/nodes/pve/lxc/121":             "upid.json",
		"GET /api2/json/nodes/pve/tasks":                  "tasks.json",
	})
	point(t, srv.URL)

	_, _, _ = run(t, "lxc", "rm", "121", "--node", "pve", "--yes", "--force-unmanaged")

	var deleted bool
	for _, req := range srv.Requests {
		if req == "DELETE /api2/json/nodes/pve/lxc/121" {
			deleted = true
		}
	}
	if !deleted {
		t.Errorf("--force-unmanaged doit laisser passer l'écriture : %v", srv.Requests)
	}
}

// A password on the command line is readable by `ps` and stays in the shell
// history. The flag therefore does not exist — and this test is what keeps a
// future --password from being added back for convenience.
func TestContainerPasswordIsNotACommandLineArgument(t *testing.T) {
	_, _, err := run(t, "lxc", "create", "120", "--password", "hunter2")
	if err == nil {
		t.Fatal("--password ne doit pas exister")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("erreur inattendue : %v", err)
	}
}
