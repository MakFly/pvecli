package pve

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// serveFixture replays a response captured from the lab node.
func serveFixture(t *testing.T, path string) *Client {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

func TestNodesDecodesRealAnswer(t *testing.T) {
	c := serveFixture(t, "../../testdata/nodes.json")

	nodes, err := c.Nodes(context.Background())
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("%d nœuds, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Node != "pve" || n.Status != "online" {
		t.Errorf("nœud = %q, statut = %q", n.Node, n.Status)
	}
	if n.MaxCPU != 16 {
		t.Errorf("MaxCPU = %d, want 16", n.MaxCPU)
	}
	// The lab has 32 GB: the field is a byte count, not megabytes.
	if n.MaxMem < 30<<30 || n.MaxMem > 34<<30 {
		t.Errorf("MaxMem = %d, ce champ est en octets", n.MaxMem)
	}
	// CPU is a ratio in 0..1, never a percentage.
	if n.CPU < 0 || n.CPU > 1 {
		t.Errorf("CPU = %v, attendu un ratio entre 0 et 1", n.CPU)
	}
}

func TestNodeStatusDecodesRealAnswer(t *testing.T) {
	c := serveFixture(t, "../../testdata/node-status.json")

	st, err := c.NodeStatus(context.Background(), "pve")
	if err != nil {
		t.Fatalf("NodeStatus: %v", err)
	}
	if st.PVEVersion == "" || st.KVersion == "" {
		t.Errorf("version PVE = %q, noyau = %q", st.PVEVersion, st.KVersion)
	}
	if len(st.LoadAvg) != 3 {
		t.Errorf("loadavg = %v, want 3 valeurs", st.LoadAvg)
	}
	if st.CPUInfo.Cores == 0 || st.CPUInfo.CPUs == 0 {
		t.Errorf("cpuinfo = %+v", st.CPUInfo)
	}
	if st.Memory.Total == 0 || st.RootFS.Total == 0 {
		t.Errorf("mémoire = %+v, rootfs = %+v", st.Memory, st.RootFS)
	}
}
