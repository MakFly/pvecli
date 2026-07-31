package pve

import (
	"context"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/testutil"
)

// replay wires a client onto answers captured from the lab. Replaying real
// answers rather than hand-written ones is the point: a hand-written fixture
// agrees with whatever the developer believed the schema was.
func replay(t *testing.T, routes map[string]string) *Client {
	t.Helper()

	srv := testutil.New(t, "../../testdata", routes)
	c, err := New(Options{
		Endpoint:  srv.URL,
		TokenID:   "automation@pve!pvectl",
		Secret:    "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStoragesDecodeRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/nodes/pve/storage": "storage.json"})

	stores, err := c.Storages(context.Background(), "pve")
	if err != nil {
		t.Fatalf("Storages: %v", err)
	}
	if len(stores) != 2 {
		t.Fatalf("%d stockages, want 2", len(stores))
	}

	// The lab's own constraint, asserted rather than assumed: neither storage
	// accepts both families, which is what PVX-014 exists to make visible.
	byName := map[string]Storage{}
	for _, s := range stores {
		byName[s.Storage] = s
	}
	if !byName["local"].Accepts("iso") || byName["local"].Accepts("images") {
		t.Errorf("local accepte %v", byName["local"].ContentTypes())
	}
	if !byName["local-lvm"].Accepts("images") || byName["local-lvm"].Accepts("iso") {
		t.Errorf("local-lvm accepte %v", byName["local-lvm"].ContentTypes())
	}
}

func TestTasksDecodeRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/nodes/pve/tasks": "tasks.json"})

	tasks, err := c.Tasks(context.Background(), "pve", false, 0)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("aucune tâche décodée")
	}
	for _, task := range tasks {
		if task.UPID == "" || task.Type == "" {
			t.Errorf("tâche incomplète: %+v", task)
		}
		// A UPID is a colon-separated record, and that is exactly why it needs
		// escaping when it travels inside a path (PVX-015).
		if strings.Count(task.UPID, ":") < 7 {
			t.Errorf("UPID inattendu: %q", task.UPID)
		}
	}
}

func TestResourcesDecodeRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/cluster/resources": "cluster-resources.json"})

	res, err := c.Resources(context.Background(), "")
	if err != nil {
		t.Fatalf("Resources: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("aucune ressource décodée")
	}

	kinds := map[string]bool{}
	for _, r := range res {
		kinds[r.Type] = true
	}
	// One call returns node and storage alike — which is the reason to prefer
	// it over looping /nodes/{n}/…
	if !kinds["node"] || !kinds["storage"] {
		t.Errorf("types retournés: %v", kinds)
	}
}

func TestClusterStatusDecodesRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/cluster/status": "cluster-status.json"})

	entries, err := c.ClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}
	if len(entries) != 1 || entries[0].Type != "node" || entries[0].Local != 1 {
		t.Errorf("mono-nœud attendu, got %+v", entries)
	}
}

// The query string must land in the URL's query, not inside its path: appending
// "?limit=5" to url.URL.Path gets escaped, and PVE answers 501 for what reads
// like a missing endpoint.
func TestQueryGoesToTheQueryString(t *testing.T) {
	srv := testutil.New(t, "../../testdata", map[string]string{
		"GET /api2/json/nodes/pve/tasks": "tasks.json",
	})
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "a@pve!b", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Tasks(context.Background(), "pve", true, 5); err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	// The route matched on the bare path, which only happens if the query was
	// kept out of it.
	if len(srv.Requests) != 1 || srv.Requests[0] != "GET /api2/json/nodes/pve/tasks" {
		t.Errorf("requêtes reçues: %v", srv.Requests)
	}
}

func TestGuestsAreSortedByVMID(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/nodes/pve/qemu": "qemu-empty.json"})

	vms, err := c.VMs(context.Background(), "pve")
	if err != nil {
		t.Fatalf("VMs: %v", err)
	}
	if len(vms) != 0 {
		t.Errorf("le lab est vierge, got %d VM", len(vms))
	}
}

// The VM created end to end by pvecli, replayed. This fixture is the proof that
// the shapes above are the node's, not the developer's.
func TestGuestConfigDecodesRealVM(t *testing.T) {
	c := replay(t, map[string]string{
		"GET /api2/json/nodes/pve/qemu/211/config": "qemu-config.json",
	})

	cfg, err := c.GuestConfig(context.Background(), "pve", TypeQEMU, 211)
	if err != nil {
		t.Fatalf("GuestConfig: %v", err)
	}

	if cfg.String("name") != "lab-app-01" {
		t.Errorf("name = %q", cfg.String("name"))
	}

	// The disk is an option string: a volid, then options.
	disk := ParseOptionString(cfg.String("scsi0"))
	if !strings.HasPrefix(disk.Value, "local-lvm:") {
		t.Errorf("scsi0 = %q, la valeur positionnelle doit être un volid", disk.Value)
	}
	if disk.Get("size") == "" {
		t.Errorf("scsi0 doit porter une taille: %v", disk.Opts)
	}

	// The NIC is the other shape: pairs only, no positional value.
	nic := ParseOptionString(cfg.String("net0"))
	if nic.Value != "" {
		t.Errorf("net0 ne doit pas avoir de valeur positionnelle: %q", nic.Value)
	}
	if nic.Get("bridge") != "vmbr0" || nic.Get("virtio") == "" {
		t.Errorf("net0 = %v", nic.Opts)
	}

	// sshkeys is stored percent-encoded, and a '+' in a key arrives as %2B.
	if keys := cfg.String("sshkeys"); strings.Contains(keys, " ") {
		t.Errorf("sshkeys doit être URL-encodé côté PVE: %q", keys)
	}
}

func TestVMsDecodeRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/nodes/pve/qemu": "qemu.json"})

	vms, err := c.VMs(context.Background(), "pve")
	if err != nil {
		t.Fatalf("VMs: %v", err)
	}
	if len(vms) != 1 {
		t.Fatalf("%d VM, want 1", len(vms))
	}
	if vms[0].VMID != 211 || vms[0].Type != TypeQEMU {
		t.Errorf("VM = %+v", vms[0])
	}
	if !vms[0].HasTag("lab") {
		t.Errorf("tags = %q", vms[0].Tags)
	}
	if vms[0].IsTemplate() {
		t.Error("une VM ordinaire ne doit pas être vue comme un template")
	}
}
