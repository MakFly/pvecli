package pve

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MakFly/pvectl/internal/testutil"
)

func TestAgentInterfacesDecodeRealAnswer(t *testing.T) {
	srv := testutil.New(t, "../../testdata", map[string]string{
		"GET /api2/json/nodes/pve/qemu/212/agent/network-get-interfaces": "agent-ifaces.json",
	})
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "a@pve!b", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	ifaces, err := c.AgentInterfaces(context.Background(), "pve", 212)
	if err != nil {
		t.Fatalf("AgentInterfaces: %v", err)
	}
	if len(ifaces) < 2 {
		t.Fatalf("%d interfaces, l'invité en expose au moins deux (lo + eth0)", len(ifaces))
	}

	var lo, eth *AgentInterface
	for i := range ifaces {
		if ifaces[i].IsLoopback() {
			lo = &ifaces[i]
		} else {
			eth = &ifaces[i]
		}
	}
	if lo == nil || eth == nil {
		t.Fatalf("interfaces = %+v", ifaces)
	}
	// The loopback never yields an address, even though it has one: it teaches
	// nothing and a script that took it would connect to itself.
	if lo.FirstIPv4() != "" {
		t.Errorf("FirstIPv4 sur lo = %q, doit être vide", lo.FirstIPv4())
	}
	if eth.FirstIPv4() == "" {
		t.Errorf("l'interface %q n'expose aucune IPv4: %+v", eth.Name, eth.IPAddresses)
	}
}

// PVE answers 500 whether the agent is missing, stopped, or the guest is off.
// Passing that through would look like an outage; it means "install a package".
func TestAgentUnavailableIsTranslated(t *testing.T) {
	srv := testutil.New(t, "../../testdata", nil) // aucune route : 404
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "a@pve!b", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A 404 is not the agent case and must stay an APIError.
	_, err = c.AgentInterfaces(context.Background(), "pve", 212)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("un 404 doit rester une APIError, got %T: %v", err, err)
	}

	// The 500 case, which is the one worth translating.
	agentErr := &AgentError{VMID: 212}
	if !errors.Is(agentErr, ErrAgentUnavailable) {
		t.Error("AgentError doit s'unwrapper vers ErrAgentUnavailable")
	}
	msg := agentErr.Error()
	for _, want := range []string{"qemu-guest-agent", "agent=1", "redémarrée"} {
		if !strings.Contains(msg, want) {
			t.Errorf("le message doit mentionner %q:\n%s", want, msg)
		}
	}
}

// "current" is a marker PVE appends, not a snapshot: rolling back to it would
// be rolling back to nothing.
func TestSnapshotCurrentIsNotASnapshot(t *testing.T) {
	if !(Snapshot{Name: "current"}).IsCurrent() {
		t.Error("« current » doit être reconnu comme le marqueur")
	}
	if (Snapshot{Name: "filet"}).IsCurrent() {
		t.Error("un vrai snapshot ne doit pas être pris pour le marqueur")
	}
}

func TestSnapshotPaths(t *testing.T) {
	cases := []struct {
		kind               GuestType
		name, action, want string
	}{
		{TypeQEMU, "", "", "/nodes/pve/qemu/212/snapshot"},
		{TypeQEMU, "filet", "", "/nodes/pve/qemu/212/snapshot/filet"},
		{TypeQEMU, "filet", "rollback", "/nodes/pve/qemu/212/snapshot/filet/rollback"},
		{TypeLXC, "filet", "rollback", "/nodes/pve/lxc/212/snapshot/filet/rollback"},
	}
	for _, tc := range cases {
		if got := SnapshotPath(tc.kind, "pve", 212, tc.name, tc.action); got != tc.want {
			t.Errorf("SnapshotPath(%s, %q, %q) = %q, want %q", tc.kind, tc.name, tc.action, got, tc.want)
		}
	}
}
