package pve

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/testutil"
)

// A mutation answers with a UPID, and the token name inside it carries a '!' —
// the character that must survive the path unescaped when the task is polled.
func TestWritesReturnAUPID(t *testing.T) {
	srv := testutil.New(t, "../../testdata", map[string]string{
		"POST /api2/json/nodes/pve/qemu":                  "upid.json",
		"POST /api2/json/nodes/pve/qemu/211/status/start": "upid.json",
		"DELETE /api2/json/nodes/pve/qemu/211":            "upid.json",
		"POST /api2/json/nodes/pve/qemu/211/config":       "upid.json",
	})
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "automation@pve!pvectl", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	upid, err := c.CreateGuest(ctx, "pve", TypeQEMU, 211, url.Values{"name": {"lab-app-01"}})
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	if !IsUPID(upid) {
		t.Fatalf("CreateGuest a renvoyé %q, want un UPID", upid)
	}

	parsed, err := ParseUPID(upid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parsed.User, "!") {
		t.Errorf("User = %q — le nom du token contient un '!'", parsed.User)
	}

	if _, err := c.SetGuestStatus(ctx, "pve", TypeQEMU, 211, ActionStart, nil); err != nil {
		t.Fatalf("SetGuestStatus: %v", err)
	}
	if _, err := c.DeleteGuest(ctx, "pve", TypeQEMU, 211, DeleteOptions{Purge: true}); err != nil {
		t.Fatalf("DeleteGuest: %v", err)
	}
}

// The three switches of a destruction are separate parameters, and one of them
// does not exist on both families.
func TestDeleteOptionsStayWhatWasAsked(t *testing.T) {
	purgeOnly := DeleteOptions{Purge: true}.Values(TypeQEMU)
	if purgeOnly.Get("purge") != "1" {
		t.Errorf("--purge doit être envoyé: %v", purgeOnly)
	}
	// --purge used to drag destroy-unreferenced-disks along with it. Wiping a
	// volume the operator never mentioned is exactly the widening PRD §7.6
	// forbids.
	if _, present := purgeOnly["destroy-unreferenced-disks"]; present {
		t.Errorf("--purge seul ne doit pas effacer les volumes non référencés: %v", purgeOnly)
	}

	// `force` is declared by the LXC endpoint only; PVE rejects unknown
	// parameters, so sending it to QEMU would fail the whole call.
	if _, present := (DeleteOptions{Force: true}).Values(TypeQEMU)["force"]; present {
		t.Error("« force » n'existe pas sur le DELETE QEMU")
	}
	if (DeleteOptions{Force: true}).Values(TypeLXC).Get("force") != "1" {
		t.Error("« force » existe sur le DELETE LXC")
	}
}

// The action path is what --dry-run shows, so it has to be the path that will
// actually be called.
func TestStatusPathMatchesTheEndpoint(t *testing.T) {
	if got, want := StatusPath(TypeQEMU, "pve", 211, ActionShutdown),
		"/nodes/pve/qemu/211/status/shutdown"; got != want {
		t.Errorf("StatusPath = %q, want %q", got, want)
	}
	if got, want := StatusPath(TypeLXC, "pve", 211, ActionStop),
		"/nodes/pve/lxc/211/status/stop"; got != want {
		t.Errorf("StatusPath = %q, want %q", got, want)
	}
	if got, want := CreatePath(TypeQEMU, "pve"), "/nodes/pve/qemu"; got != want {
		t.Errorf("CreatePath = %q, want %q", got, want)
	}
	if got, want := DeletePath(TypeQEMU, "pve", 211), "/nodes/pve/qemu/211"; got != want {
		t.Errorf("DeletePath = %q, want %q", got, want)
	}
	if got, want := CreatePath(TypeLXC, "pve"), "/nodes/pve/lxc"; got != want {
		t.Errorf("CreatePath(LXC) = %q, want %q", got, want)
	}
	if got, want := DeletePath(TypeLXC, "pve", 120), "/nodes/pve/lxc/120"; got != want {
		t.Errorf("DeletePath(LXC) = %q, want %q", got, want)
	}
}

// stop and shutdown do not lead to the same place, and neither do start and
// suspend: the target status is what tells the pipeline a write is unnecessary.
func TestActionTargetStatus(t *testing.T) {
	cases := map[GuestAction]string{
		ActionStart: "running", ActionResume: "running", ActionReboot: "running",
		ActionStop: "stopped", ActionShutdown: "stopped",
		ActionSuspend: "",
	}
	for action, want := range cases {
		if got := action.TargetStatus(); got != want {
			t.Errorf("%s.TargetStatus() = %q, want %q", action, got, want)
		}
	}
}
