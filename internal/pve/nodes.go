package pve

import (
	"context"
	"net/url"
)

// Node is one entry of GET /nodes.
//
// Schema verified on the lab node (PVE 9.2.2) with `pvesh get /nodes`, not
// written from memory — PRD §6.3. See docs/API-MAP.md.
type Node struct {
	Node   string `json:"node"`
	Status string `json:"status"`

	// CPU is a load ratio between 0 and 1, not a percentage and not a count.
	// MaxCPU is the number of cores.
	CPU    float64 `json:"cpu"`
	MaxCPU int     `json:"maxcpu"`

	Mem     int64 `json:"mem"`
	MaxMem  int64 `json:"maxmem"`
	Disk    int64 `json:"disk"`
	MaxDisk int64 `json:"maxdisk"`
	Uptime  int64 `json:"uptime"`

	SSLFingerprint string `json:"ssl_fingerprint"`
}

// Nodes lists the nodes of the cluster.
//
// GET /nodes
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var out []Node
	if err := c.get(ctx, epNodes, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// NodeStatus is GET /nodes/{node}/status. Only the fields the CLI shows are
// declared; PVE returns a great deal more.
type NodeStatus struct {
	Uptime     int64    `json:"uptime"`
	CPU        float64  `json:"cpu"`
	LoadAvg    []string `json:"loadavg"`
	KVersion   string   `json:"kversion"`
	PVEVersion string   `json:"pveversion"`

	CPUInfo struct {
		Cores   int    `json:"cores"`
		CPUs    int    `json:"cpus"`
		Sockets int    `json:"sockets"`
		Model   string `json:"model"`
	} `json:"cpuinfo"`

	Memory struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
		Free  int64 `json:"free"`
	} `json:"memory"`

	RootFS struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
		Avail int64 `json:"avail"`
	} `json:"rootfs"`
}

// NodeStatus reads one node's detailed status.
//
// GET /nodes/{node}/status
func (c *Client) NodeStatus(ctx context.Context, node string) (*NodeStatus, error) {
	var out NodeStatus
	if err := c.get(ctx, epNodeStatus, []string{node}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RebootNode asks the node to reboot itself.
//
// POST /nodes/{node}/status  command=reboot
//
// Privilege is Sys.PowerMgmt on /nodes/{node} — NOT Sys.Modify, which is what
// most of the node surface takes. A token that can rewrite the APT sources of
// a node still cannot power-cycle it, and that separation is deliberate.
//
// The call is synchronous: it returns no UPID, because the node cannot report
// on a task whose whole point is that the node stops answering. Its return is
// therefore an acceptance and nothing more — the proof has to be gathered
// afterwards, from the outside.
func (c *Client) RebootNode(ctx context.Context, node string) error {
	body := url.Values{}
	body.Set("command", "reboot")
	return c.post(ctx, epNodePower, []string{node}, body, nil)
}

// Version is the payload of GET /version.
//
// Schema verified on the lab node (PVE 9.2.2) with `pvesh get /version`.
type Version struct {
	Version string `json:"version"`
	Release string `json:"release"`
	RepoID  string `json:"repoid"`
}

// Version reads the node's own version, release and repository id.
//
// GET /version
func (c *Client) Version(ctx context.Context) (*Version, error) {
	var out Version
	if err := c.get(ctx, epVersion, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ClusterNode is one entry of GET /cluster/status. On a single-node install
// there is exactly one, of type "node".
type ClusterNode struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	IP     string `json:"ip"`
	NodeID int    `json:"nodeid"`
	Online int    `json:"online"`
	Local  int    `json:"local"`
}

// ClusterStatus reads the cluster membership.
//
// GET /cluster/status
func (c *Client) ClusterStatus(ctx context.Context) ([]ClusterNode, error) {
	var out []ClusterNode
	if err := c.get(ctx, epClusterStat, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Permissions reads the effective privileges of the authenticated identity,
// as a map of path to privilege to propagation bit.
//
// path narrows the answer to one ACL path — and it must be the API doing that
// narrowing, not the caller. The full dump only lists paths where an ACL was
// posted; asking for "/vms/120" makes the node RESOLVE inheritance and answer
// what actually applies there. Filtering the dump client-side reports "no
// privilege" on a guest whose rights come, correctly, from a propagated ACL on
// "/vms".
//
// GET /access/permissions
func (c *Client) Permissions(ctx context.Context, path string) (map[string]map[string]int, error) {
	var query url.Values
	if path != "" {
		query = url.Values{"path": {path}}
	}
	var out map[string]map[string]int
	if err := c.get(ctx, epPermissions, nil, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}
