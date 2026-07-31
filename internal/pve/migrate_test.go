package pve

import (
	"context"
	"net/http"
	"testing"
)

// The two families spell the SAME fields differently: QEMU answers
// allowed_nodes / not_allowed_nodes with underscores, LXC answers
// allowed-nodes / not-allowed-nodes with hyphens. One struct reused for both
// decodes silently empty on one of the two — and an empty allowed_nodes is
// exactly what « you cannot migrate » looks like, so the bug would hide behind
// a plausible answer. Both fixtures come from the lab.
func TestMigratePrecheckReadsBothSpellings(t *testing.T) {
	t.Run("qemu", func(t *testing.T) {
		c := serveFixture(t, "../../testdata/migrate-precheck-qemu.json")

		pre, err := c.MigratePreconditions(context.Background(), "pve", TypeQEMU, 211, "")
		if err != nil {
			t.Fatalf("MigratePreconditions: %v", err)
		}
		if !pre.Running {
			t.Error("la VM 211 tournait au moment de la capture")
		}
		if len(pre.LocalDisks) == 0 {
			t.Fatal("local_disks vide — les disques de 211 sont sur local-lvm, non partagé")
		}
		// A cloudinit drive is a CD-ROM, and the disk itself is not: only the
		// second has to travel.
		movable := pre.MovableDisks()
		if len(movable) == 0 {
			t.Error("aucun disque à déplacer alors que la VM en a de locaux")
		}
		for _, d := range movable {
			if d.VolID == "" {
				t.Errorf("volid vide dans %+v", d)
			}
		}
		// Mono-node: nowhere to go, and the node says so by an empty list.
		if len(pre.AllowedNodes) != 0 {
			t.Errorf("allowed_nodes = %v sur un lab mono-nœud", pre.AllowedNodes)
		}
	})

	t.Run("lxc", func(t *testing.T) {
		c := serveFixture(t, "../../testdata/migrate-precheck-lxc.json")

		pre, err := c.MigratePreconditions(context.Background(), "pve", TypeLXC, 120, "")
		if err != nil {
			t.Fatalf("MigratePreconditions: %v", err)
		}
		if pre.Running {
			t.Error("le conteneur 120 était arrêté au moment de la capture")
		}
		if pre.AllowedNodes == nil {
			t.Error("allowed-nodes (avec un tiret) n'a pas été décodé — le champ LXC est ignoré")
		}
		if pre.NotAllowedNode == nil {
			t.Error("not-allowed-nodes (avec un tiret) n'a pas été décodé")
		}
	})
}

func TestMigratePrecheckKeepsTheRefusalReason(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"running":1,"allowed_nodes":["pve3"],
			"not_allowed_nodes":{"pve2":"missing storage 'local-lvm'"},
			"local_resources":["hostpci0"]}}`))
	})

	pre, err := c.MigratePreconditions(context.Background(), "pve", TypeQEMU, 211, "pve2")
	if err != nil {
		t.Fatalf("MigratePreconditions: %v", err)
	}
	// The reason is the whole value of the endpoint: « refused » alone sends
	// the operator guessing.
	if pre.NotAllowedNode["pve2"] == "" {
		t.Error("la raison du refus est perdue")
	}
	if len(pre.LocalResources) != 1 {
		t.Errorf("local_resources = %v — un périphérique local cloue le guest sur son nœud", pre.LocalResources)
	}
}

// A container has no live migration: it carries `restart`, a VM carries
// `online`. Sending one to the other endpoint is an unknown parameter.
func TestMigrateValuesDifferByGuestType(t *testing.T) {
	o := MigrateOptions{
		Target: "pve2", Online: true, WithLocalDisks: true,
		Restart: true, Timeout: 60, BWLimit: 10240, TargetStorage: "1",
	}

	vm := o.Values(TypeQEMU)
	if vm.Get("online") != "1" || vm.Get("with-local-disks") != "1" {
		t.Errorf("payload VM = %v", vm)
	}
	if vm.Has("restart") || vm.Has("timeout") {
		t.Errorf("payload VM = %v — restart/timeout sont des paramètres LXC", vm)
	}

	ct := o.Values(TypeLXC)
	if ct.Get("restart") != "1" || ct.Get("timeout") != "60" {
		t.Errorf("payload LXC = %v", ct)
	}
	if ct.Has("online") || ct.Has("with-local-disks") {
		t.Errorf("payload LXC = %v — un conteneur ne migre pas à chaud", ct)
	}

	// Shared by both.
	for _, v := range []map[string][]string{vm, ct} {
		if v["target"][0] != "pve2" || v["bwlimit"][0] != "10240" || v["targetstorage"][0] != "1" {
			t.Errorf("paramètres communs manquants dans %v", v)
		}
	}
}

func TestMigrateGuestUsesTheRightEndpoint(t *testing.T) {
	for _, tc := range []struct {
		kind GuestType
		want string
	}{
		{TypeQEMU, "/api2/json/nodes/pve/qemu/211/migrate"},
		{TypeLXC, "/api2/json/nodes/pve/lxc/120/migrate"},
	} {
		var path string
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":"UPID:pve:0011A2B3:00A1B2C3:6889F0AA:qmigrate:211:automation@pve!pvectl:"}`))
		})

		vmid := 211
		if tc.kind == TypeLXC {
			vmid = 120
		}
		if _, err := c.MigrateGuest(context.Background(), "pve", tc.kind, vmid,
			MigrateOptions{Target: "pve2"}); err != nil {
			t.Fatalf("MigrateGuest: %v", err)
		}
		if path != tc.want {
			t.Errorf("chemin = %q, want %q", path, tc.want)
		}
	}
}
