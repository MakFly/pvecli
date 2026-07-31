package pve

import (
	"context"
	"testing"

	"github.com/MakFly/pvecli/internal/testutil"
)

// The payload is what --dry-run shows and what gets sent: they are the same
// function, so they cannot drift apart.
func TestCloneOptionsValues(t *testing.T) {
	full := CloneOptions{NewID: 212, Name: "lab-app-02", Full: true, Storage: "local-lvm"}.Values(TypeQEMU)

	if full.Get("newid") != "212" || full.Get("name") != "lab-app-02" {
		t.Errorf("payload = %v", full)
	}
	if full.Get("full") != "1" {
		t.Errorf("--full doit être envoyé explicitement: %v", full)
	}

	// A linked clone sends no `full` at all: PVE's default is the linked one,
	// and sending full=0 would be a value the schema does not expect.
	linked := CloneOptions{NewID: 212}.Values(TypeQEMU)
	if _, present := linked["full"]; present {
		t.Errorf("un clone lié ne doit pas envoyer « full »: %v", linked)
	}
	if linked.Get("name") != "" {
		t.Errorf("une option vide ne doit pas être envoyée: %v", linked)
	}
}

// The two families name the new guest differently, and the difference is in the
// schema, not in a habit: sending `name` to the LXC endpoint is rejected.
func TestCloneNamesTheGuestPerFamily(t *testing.T) {
	ct := CloneOptions{NewID: 121, Name: "web2"}.Values(TypeLXC)

	if ct.Get("hostname") != "web2" {
		t.Errorf("un clone LXC est nommé par « hostname »: %v", ct)
	}
	if _, present := ct["name"]; present {
		t.Errorf("« name » n'existe pas sur le clone LXC: %v", ct)
	}
}

func TestCloneAndTemplateReturnUPIDs(t *testing.T) {
	srv := testutil.New(t, "../../testdata", map[string]string{
		"POST /api2/json/nodes/pve/qemu/9000/clone":    "upid.json",
		"POST /api2/json/nodes/pve/qemu/9000/template": "upid.json",
	})
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "automation@pve!pvectl", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	upid, err := c.CloneGuest(ctx, "pve", TypeQEMU, 9000, CloneOptions{NewID: 212, Full: true})
	if err != nil || !IsUPID(upid) {
		t.Fatalf("CloneGuest = %q, %v", upid, err)
	}
	if upid, err = c.TemplateVM(ctx, "pve", 9000); err != nil || !IsUPID(upid) {
		t.Fatalf("TemplateVM = %q, %v", upid, err)
	}
}

func TestLifecyclePaths(t *testing.T) {
	if got, want := ClonePath(TypeQEMU, "pve", 9000), "/nodes/pve/qemu/9000/clone"; got != want {
		t.Errorf("ClonePath = %q, want %q", got, want)
	}
	if got, want := TemplatePath("pve", 9000), "/nodes/pve/qemu/9000/template"; got != want {
		t.Errorf("TemplatePath = %q, want %q", got, want)
	}
	if got, want := ConfigPath(TypeQEMU, "pve", 212), "/nodes/pve/qemu/212/config"; got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}

	// The path shown by --dry-run is the path that will be called, for both
	// families: a helper that silently fell back to /qemu would make a
	// container command lie about what it does.
	if got, want := ClonePath(TypeLXC, "pve", 120), "/nodes/pve/lxc/120/clone"; got != want {
		t.Errorf("ClonePath(LXC) = %q, want %q", got, want)
	}
	if got, want := ConfigPath(TypeLXC, "pve", 120), "/nodes/pve/lxc/120/config"; got != want {
		t.Errorf("ConfigPath(LXC) = %q, want %q", got, want)
	}
}
