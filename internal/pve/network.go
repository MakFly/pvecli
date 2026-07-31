package pve

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// NetIface is one entry of GET /nodes/{node}/network.
//
// Schema read on the node (`pvesh usage /nodes/pve/network -v`) and against a
// real answer. Two fields are worth naming, because they are not the same
// question:
//
//	exists  the device is present on the running system
//	active  the device is up
//
// Neither of them says whether the CONFIGURATION on disk matches what is
// running. That answer is not in this struct at all — see NetworkConfig.
type NetIface struct {
	Iface string `json:"iface"`
	Type  string `json:"type"`

	Method  string `json:"method,omitempty"`
	Method6 string `json:"method6,omitempty"`

	Active    int `json:"active,omitempty"`
	Exists    int `json:"exists,omitempty"`
	Autostart int `json:"autostart,omitempty"`

	CIDR    string `json:"cidr,omitempty"`
	Address string `json:"address,omitempty"`
	Netmask string `json:"netmask,omitempty"`
	Gateway string `json:"gateway,omitempty"`

	BridgePorts string `json:"bridge_ports,omitempty"`
	BridgeVLAN  string `json:"bridge_vlan_aware,omitempty"`
	BondSlaves  string `json:"slaves,omitempty"`
	BondMode    string `json:"bond_mode,omitempty"`
	VLANRawDev  string `json:"vlan-raw-device,omitempty"`
	VLANID      string `json:"vlan-id,omitempty"`

	Priority int      `json:"priority,omitempty"`
	Comments string   `json:"comments,omitempty"`
	Families []string `json:"families,omitempty"`
	Altnames []string `json:"altnames,omitempty"`
}

// Ports renders whatever this interface is built on top of, whichever field
// carries it.
func (n NetIface) Ports() string {
	switch {
	case n.BridgePorts != "":
		return n.BridgePorts
	case n.BondSlaves != "":
		return n.BondSlaves
	case n.VLANRawDev != "":
		return n.VLANRawDev
	default:
		return ""
	}
}

// NetworkConfig is the whole answer of GET /nodes/{node}/network — and it is
// the one endpoint in this CLI whose answer does not fit inside `data`.
//
// PVE hands the pending diff back through set_result_attrib('changes', …)
// (PVE/API2/Network.pm:418), which lands as a SIBLING of `data` in the JSON
// envelope. A client that unwraps `data` and stops there cannot see that the
// node has network changes waiting — which is precisely the thing an operator
// must see before touching anything.
type NetworkConfig struct {
	Ifaces []NetIface `json:"ifaces"`

	// Changes is the unified diff between /etc/network/interfaces and
	// /etc/network/interfaces.new. Empty when nothing is pending.
	Changes string `json:"changes,omitempty"`
}

// Pending reports whether the node carries network changes not yet applied.
func (c NetworkConfig) Pending() bool { return strings.TrimSpace(c.Changes) != "" }

// PendingIfaces names the interfaces the pending diff touches.
//
// The diff is a plain unified diff of an interfaces(5) file, so an added or
// removed line belongs to whichever `iface <name>` stanza precedes it —
// including when that stanza header is itself a context line. Tracking the
// last stanza seen is what attributes « +bridge-ports nic0 nic1 » to vmbr0
// instead of to nothing.
func (c NetworkConfig) PendingIfaces() map[string]bool {
	touched := map[string]bool{}
	current := ""

	for _, line := range strings.Split(c.Changes, "\n") {
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@") {
			continue
		}

		// Strip the diff marker, then look for a stanza header. A header on a
		// +/- line both opens a stanza and is itself a change.
		body := line
		changed := false
		if len(line) > 0 && (line[0] == '+' || line[0] == '-') {
			body, changed = line[1:], true
		} else if len(line) > 0 && line[0] == ' ' {
			body = line[1:]
		}

		if name, ok := ifaceStanza(body); ok {
			current = name
		}
		if changed && current != "" && strings.TrimSpace(body) != "" {
			touched[current] = true
		}
	}
	return touched
}

// ifaceStanza reads the interface name out of an « iface eth0 inet static »
// or « auto vmbr0 » line.
func ifaceStanza(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", false
	}
	switch fields[0] {
	case "iface", "auto", "allow-hotplug":
		return fields[1], true
	default:
		return "", false
	}
}

// Network lists the interfaces of a node, together with the pending diff.
//
// GET /nodes/{node}/network
func (c *Client) Network(ctx context.Context, node, kind string) (*NetworkConfig, error) {
	var query url.Values
	if kind != "" {
		query = url.Values{"type": {kind}}
	}

	raw, err := c.getRaw(ctx, epNetwork, []string{node}, query)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Data    []NetIface `json:"data"`
		Changes string     `json:"changes"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	return &NetworkConfig{Ifaces: envelope.Data, Changes: envelope.Changes}, nil
}

// NetworkIface details one interface.
//
// The answer carries no `iface` key — the name was in the path, so PVE does
// not repeat it. Filling it back in here keeps the struct usable by callers
// that never saw the request.
//
// GET /nodes/{node}/network/{iface}
func (c *Client) NetworkIface(ctx context.Context, node, iface string) (*NetIface, error) {
	var out NetIface
	if err := c.get(ctx, epNetworkIface, []string{node, iface}, nil, &out); err != nil {
		return nil, err
	}
	out.Iface = iface
	return &out, nil
}

// ApplyNetwork applies the pending configuration and returns the UPID of the
// reload task.
//
// PUT /nodes/{node}/network — « Reload network configuration ». This is the
// endpoint that can cut the node off the network.
func (c *Client) ApplyNetwork(ctx context.Context, node string) (string, error) {
	var upid string
	if err := c.post(ctx, epNetworkApply, []string{node}, url.Values{}, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// RevertNetwork throws the pending changes away. Synchronous: it unlinks
// /etc/network/interfaces.new and answers nothing.
//
// DELETE /nodes/{node}/network
func (c *Client) RevertNetwork(ctx context.Context, node string) error {
	return c.del(ctx, epNetworkRevert, []string{node}, nil, nil)
}

// NetworkApplyPath and NetworkRevertPath render the paths, for --dry-run.
func NetworkApplyPath(node string) string  { return epNetworkApply.Path(node) }
func NetworkRevertPath(node string) string { return epNetworkRevert.Path(node) }
