package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakFly/pvectl/internal/testutil"
	"github.com/spf13/cobra"
)

// notDeclaredByTerraform lists the guest commands that write without touching a
// DECLARED configuration, and says for each why it is out of the guard's reach.
//
// This is the exemption list of PRD §5.4, and it is keyed by full command path
// on purpose: `vm rm` is guarded, `vm snapshot rm` is not, and a list keyed by
// leaf name could not tell them apart.
var notDeclaredByTerraform = map[string]string{
	"vm create":  "rien n'existe encore — il n'y a pas de propriétaire à opposer",
	"lxc create": "idem",

	// Execution state. Terraform declares `on_boot`, not whether the guest is
	// running right now. Refusing a start would make the guard an obstacle to
	// operating the lab, for no drift avoided.
	"vm start":    "état d'exécution, pas configuration déclarée",
	"vm stop":     "état d'exécution",
	"vm shutdown": "état d'exécution",
	"vm reboot":   "état d'exécution",
	"vm reset":    "état d'exécution",
	"vm suspend":  "état d'exécution",
	"vm resume":   "état d'exécution",

	"lxc start":    "état d'exécution",
	"lxc stop":     "état d'exécution",
	"lxc shutdown": "état d'exécution",
	"lxc reboot":   "état d'exécution",
	"lxc reset":    "état d'exécution",

	// The lab's main.tf declares no snapshot resource, and the bpg provider has
	// none: taking or deleting one changes nothing Terraform believes. Rolling
	// back is the exception, because a PVE snapshot carries the configuration.
	"vm snapshot create":  "un snapshot n'est pas une ressource déclarée",
	"vm snapshot rm":      "idem",
	"lxc snapshot create": "idem",
	"lxc snapshot rm":     "idem",
}

// The criterion of PVX-041: no W command escapes the guard.
//
// Walking the actual Cobra tree rather than a hand-kept list is the whole
// point. A future `vm migrate` (M7) joins the tree with --dry-run and no
// --force-unmanaged, and this test fails the day it is added — which is the
// only moment at which the question "should the guard apply?" is cheap to
// answer.
func TestNoGuestWriteCommandEscapesTheOwnershipGuard(t *testing.T) {
	root := NewRootCmd("dev", "abc1234")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}

		// A write is a command carrying the guardrails of addWriteFlags. That
		// is the same signal the mutation pipeline uses, so the two cannot
		// disagree about what counts as a write.
		if c.Flags().Lookup("dry-run") == nil {
			return
		}
		path := strings.TrimPrefix(c.CommandPath(), "pvectl ")
		if _, exempt := notDeclaredByTerraform[path]; exempt {
			return
		}
		if c.Flags().Lookup("force-unmanaged") == nil {
			t.Errorf("« pvectl %s » écrit sans garde de propriété : "+
				"ajoute addOwnershipFlag(c) et un owner.check() dans son pre-read, "+
				"ou inscris-la dans notDeclaredByTerraform en disant pourquoi", path)
		}
	}

	for _, family := range []string{"vm", "lxc"} {
		sub, _, err := root.Find([]string{family})
		if err != nil {
			t.Fatalf("famille %q introuvable : %v", family, err)
		}
		walk(sub)
	}
}

// An exemption that names a command which no longer exists is an exemption that
// has stopped protecting anything — and it would silently absolve whatever
// takes that name next.
func TestEveryExemptionNamesARealCommand(t *testing.T) {
	root := NewRootCmd("dev", "abc1234")

	for path, why := range notDeclaredByTerraform {
		c, _, err := root.Find(strings.Split(path, " "))
		if err != nil || c.CommandPath() != "pvectl "+path {
			t.Errorf("l'exemption « %s » (%s) ne correspond à aucune commande", path, why)
		}
	}
}

// managedRoutes answers the pre-reads of every guarded command for VM 210,
// which the fixtures tag lab;managed;terraform.
func managedRoutes() map[string]string {
	return map[string]string{
		"GET /api2/json/nodes/pve/qemu/210/status/current": "qemu-status-managed.json",
		"GET /api2/json/nodes/pve/qemu/210/config":         "qemu-config-managed.json",
		"GET /api2/json/nodes/pve/qemu/210/snapshot":       "snapshots.json",

		// Deliberately routed: if a guard failed to fire, the write would
		// succeed rather than 404, and the test would be proving the wrong
		// thing — that the node refused, not that pvectl did.
		"PUT /api2/json/nodes/pve/qemu/210/config":                       "upid.json",
		"POST /api2/json/nodes/pve/qemu/210/clone":                       "upid.json",
		"POST /api2/json/nodes/pve/qemu/210/template":                    "upid.json",
		"POST /api2/json/nodes/pve/qemu/210/snapshot/avant-maj/rollback": "upid.json",
		"DELETE /api2/json/nodes/pve/qemu/210":                           "upid.json",
	}
}

// Every write that changes a declared configuration is refused, and refused in
// the pre-read — before a single mutation leaves the process.
func TestManagedGuardCoversEveryDeclaredConfigurationWrite(t *testing.T) {
	cases := map[string][]string{
		"set":               {"vm", "set", "210", "--cores", "4"},
		"clone":             {"vm", "clone", "210", "--newid", "211"},
		"template":          {"vm", "template", "210"},
		"snapshot rollback": {"vm", "snapshot", "rollback", "210", "avant-maj"},
		"rm":                {"vm", "rm", "210"},

		// A migration looks like an operation and is a declaration: the bpg
		// provider carries node_name, so moving 210 elsewhere leaves the
		// state describing a machine that is no longer there. This is the
		// command the M6 harness predicted would arrive with M7.
		"migrate": {"vm", "migrate", "210", "--target", "pve2"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			srv := testutil.New(t, "../testdata", managedRoutes())
			point(t, srv.URL)

			_, _, err := run(t, append(args, "--node", "pve", "--yes")...)
			if err == nil {
				t.Fatal("une écriture de configuration sur un guest « managed » doit être refusée")
			}
			if !strings.Contains(err.Error(), "appartient à Terraform") {
				t.Errorf("le refus doit nommer le propriétaire : %v", err)
			}

			for _, req := range srv.Requests {
				if !strings.HasPrefix(req, "GET ") {
					t.Fatalf("une écriture a été émise malgré la garde : %v", srv.Requests)
				}
			}
		})
	}
}

// The other half of the contract, and the one that keeps the guard usable:
// starting a VM Terraform owns is legitimate. Terraform declares how the guest
// is built, not whether it is powered on.
func TestExecutionStateStaysAllowedOnAManagedGuest(t *testing.T) {
	routes := managedRoutes()
	routes["POST /api2/json/nodes/pve/qemu/210/status/stop"] = "upid.json"
	routes["GET /api2/json/nodes/pve/tasks"] = "tasks.json"

	srv := testutil.New(t, "../testdata", routes)
	point(t, srv.URL)

	_, _, _ = run(t, "vm", "stop", "210", "--node", "pve", "--yes")

	var stopped bool
	for _, req := range srv.Requests {
		if req == "POST /api2/json/nodes/pve/qemu/210/status/stop" {
			stopped = true
		}
	}
	if !stopped {
		t.Errorf("la garde ne doit pas bloquer un changement d'état d'exécution : %v", srv.Requests)
	}
}

// The tag is a convention of the lab repository, not a property of Proxmox. A
// team that tags its resources `iac` must be able to say so — and, just as
// importantly, `managed` must stop meaning anything once they have.
//
// VM 210 is tagged lab;managed;terraform in the fixtures, so both halves read
// the same guest and differ only by what the configuration calls ownership.
func TestManagedTagIsConfigurable(t *testing.T) {
	withTag := func(t *testing.T, tag string) *testutil.Server {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(
			"current_context: test\ncontexts:\n  test:\n    iac:\n      managed_tag: "+tag+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PVECTL_CONFIG", path)

		routes := managedRoutes()
		routes["GET /api2/json/nodes/pve/tasks"] = "tasks.json"
		srv := testutil.New(t, "../testdata", routes)
		point(t, srv.URL)
		return srv
	}

	t.Run("le tag configuré est celui qui garde", func(t *testing.T) {
		withTag(t, "terraform")

		_, _, err := run(t, "vm", "set", "210", "--cores", "4", "--node", "pve", "--yes")
		if err == nil || !strings.Contains(err.Error(), "« terraform »") {
			t.Errorf("la garde doit suivre iac.managed_tag : %v", err)
		}
	})

	t.Run("« managed » ne garde plus rien une fois remplacé", func(t *testing.T) {
		srv := withTag(t, "possede-par-pulumi")

		_, _, _ = run(t, "vm", "set", "210", "--cores", "4", "--node", "pve", "--yes")

		var written bool
		for _, req := range srv.Requests {
			if req == "PUT /api2/json/nodes/pve/qemu/210/config" {
				written = true
			}
		}
		if !written {
			t.Errorf("un tag qui n'est plus celui configuré ne doit rien bloquer : %v", srv.Requests)
		}
	})
}

// --force-unmanaged lets the write through, and says out loud what it just cost
// the operator. A silent override would be the more dangerous flag.
func TestForceUnmanagedWarnsThatTheStateIsNowStale(t *testing.T) {
	routes := managedRoutes()
	routes["GET /api2/json/nodes/pve/tasks"] = "tasks.json"

	srv := testutil.New(t, "../testdata", routes)
	point(t, srv.URL)

	_, stderr, _ := run(t, "vm", "set", "210", "--cores", "4", "--node", "pve", "--yes", "--force-unmanaged")

	if !strings.Contains(stderr, "terraform refresh") {
		t.Errorf("le contournement doit rappeler qu'il faut rattraper le state : %q", stderr)
	}

	var written bool
	for _, req := range srv.Requests {
		if req == "PUT /api2/json/nodes/pve/qemu/210/config" {
			written = true
		}
	}
	if !written {
		t.Errorf("--force-unmanaged doit laisser passer l'écriture : %v", srv.Requests)
	}
}
