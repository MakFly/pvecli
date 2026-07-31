package pve

import (
	"context"
	"net/http"
	"testing"
)

func TestNetworkDecodesRealAnswer(t *testing.T) {
	c := serveFixture(t, "../../testdata/network.json")

	cfg, err := c.Network(context.Background(), "pve", "")
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	if len(cfg.Ifaces) == 0 {
		t.Fatal("aucune interface décodée")
	}
	if cfg.Pending() {
		t.Errorf("Pending() = true sur une capture sans modification en attente")
	}

	var bridge *NetIface
	for i := range cfg.Ifaces {
		if cfg.Ifaces[i].Type == "bridge" {
			bridge = &cfg.Ifaces[i]
			break
		}
	}
	if bridge == nil {
		t.Fatal("aucun pont dans la capture")
	}
	if bridge.CIDR == "" || bridge.Gateway == "" {
		t.Errorf("pont %s : cidr = %q, gateway = %q", bridge.Iface, bridge.CIDR, bridge.Gateway)
	}
	if bridge.Ports() == "" {
		t.Errorf("pont %s sans port — bridge_ports non décodé", bridge.Iface)
	}
	if bridge.Active != 1 {
		t.Errorf("pont %s : active = %d, la capture vient d'un nœud joignable", bridge.Iface, bridge.Active)
	}
}

// The pending diff does not live in `data`: it is a sibling key that
// set_result_attrib drops into the envelope. This is the test that would fail
// the day the client goes back to unwrapping `data` and nothing else.
func TestNetworkReadsChangesOutsideData(t *testing.T) {
	c := serveFixture(t, "../../testdata/network-pending.json")

	cfg, err := c.Network(context.Background(), "pve", "")
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	if !cfg.Pending() {
		t.Fatal("Pending() = false alors que la capture porte un diff en attente")
	}

	pending := cfg.PendingIfaces()
	// vmbr9 is added, nic1 is moved, wlp6s0 appears. vmbr0 is only context.
	for _, want := range []string{"vmbr9", "nic1", "wlp6s0"} {
		if !pending[want] {
			t.Errorf("%s devrait être marquée en attente, pending = %v", want, pending)
		}
	}
	if pending["vmbr0"] {
		t.Errorf("vmbr0 n'est touchée que par des lignes de contexte — la marquer est un faux positif")
	}
	if pending["lo"] {
		t.Errorf("lo n'est pas modifiée")
	}
}

// An added attribute inside an existing stanza belongs to that stanza, whose
// header is a context line. Losing that is how « +bridge-ports nic0 nic1 »
// ends up attributed to nobody.
func TestPendingIfacesAttributesToTheEnclosingStanza(t *testing.T) {
	cfg := NetworkConfig{Changes: `--- /etc/network/interfaces
+++ /etc/network/interfaces.new
@@ -1,6 +1,6 @@
 auto vmbr0
 iface vmbr0 inet static
 	address 192.0.2.10/24
-	bridge-ports nic0
+	bridge-ports nic0 nic1
 	bridge-stp off
`}

	pending := cfg.PendingIfaces()
	if !pending["vmbr0"] {
		t.Errorf("vmbr0 = %v, want true : la ligne modifiée est dans sa strophe", pending)
	}
	if len(pending) != 1 {
		t.Errorf("pending = %v, une seule interface est touchée", pending)
	}
}

func TestNetworkIfaceFillsBackTheName(t *testing.T) {
	// PVE does not repeat the interface name in the answer: it was in the path.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"bridge","method":"static","cidr":"192.0.2.10/24"}}`))
	})

	iface, err := c.NetworkIface(context.Background(), "pve", "vmbr0")
	if err != nil {
		t.Fatalf("NetworkIface: %v", err)
	}
	if iface.Iface != "vmbr0" {
		t.Errorf("Iface = %q, want vmbr0 — le nom vient du chemin, pas de la réponse", iface.Iface)
	}
}

func TestApplyNetworkReturnsUPID(t *testing.T) {
	var method, path string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"UPID:pve:0011A2B3:00A1B2C3:6889F0AA:srvreload:networking:automation@pve!pvectl:"}`))
	})

	upid, err := c.ApplyNetwork(context.Background(), "pve")
	if err != nil {
		t.Fatalf("ApplyNetwork: %v", err)
	}
	if method != http.MethodPut {
		t.Errorf("méthode = %s, want PUT", method)
	}
	if path != "/api2/json/nodes/pve/network" {
		t.Errorf("chemin = %s", path)
	}
	if !IsUPID(upid) {
		t.Errorf("ApplyNetwork = %q, attendu un UPID à suivre", upid)
	}
}

func TestRevertNetworkIsADeleteWithNoBody(t *testing.T) {
	var method string
	var contentType string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		contentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	})

	if err := c.RevertNetwork(context.Background(), "pve"); err != nil {
		t.Fatalf("RevertNetwork: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("méthode = %s, want DELETE", method)
	}
	// A DELETE carrying a form body earns a 501 from PVE's own HTTP server,
	// before the schema layer is reached (PVX-031).
	if contentType != "" {
		t.Errorf("Content-Type = %q sur un DELETE — il ne doit pas porter de corps", contentType)
	}
}
