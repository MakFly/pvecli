package pve

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// AgentInterface is one entry of the guest agent's network report.
//
// This endpoint is the only way PVE learns a DHCP guest's address: the
// hypervisor sees a MAC on a bridge and nothing more. Everything downstream —
// the Ansible inventory of PVX-042 above all — hangs on the agent answering.
type AgentInterface struct {
	Name            string `json:"name"`
	HardwareAddress string `json:"hardware-address,omitempty"`
	IPAddresses     []struct {
		Type    string `json:"ip-address-type"`
		Address string `json:"ip-address"`
		Prefix  int    `json:"prefix"`
	} `json:"ip-addresses,omitempty"`
}

// IsLoopback reports whether this is the guest's loopback interface.
func (i AgentInterface) IsLoopback() bool { return i.Name == "lo" }

// FirstIPv4 returns the first non-loopback IPv4 address, or "".
func (i AgentInterface) FirstIPv4() string {
	if i.IsLoopback() {
		return ""
	}
	for _, addr := range i.IPAddresses {
		if addr.Type == "ipv4" && !strings.HasPrefix(addr.Address, "127.") {
			return addr.Address
		}
	}
	return ""
}

// ErrAgentUnavailable is returned when the guest agent is not answering. It is
// its own error because the raw failure — a 500 quoting a QMP timeout — reads
// like a broken hypervisor when it means "install a package in the guest".
var ErrAgentUnavailable = errors.New("agent QEMU indisponible")

// AgentError explains an unreachable agent.
type AgentError struct {
	VMID int
	Err  error
}

func (e *AgentError) Error() string {
	return strings.TrimSpace(`l'agent QEMU de la VM ` + strconv.Itoa(e.VMID) + ` ne répond pas.

PVE ne connaît pas l'adresse IP d'une VM en DHCP : seul l'agent invité peut la
lui dire. Deux conditions, toutes les deux nécessaires :

  · côté PVE   : agent=1 dans la configuration
                 pvecli vm set ` + strconv.Itoa(e.VMID) + ` --set agent=1
  · côté invité : le paquet installé ET démarré
                 sudo apt install -y qemu-guest-agent
                 sudo systemctl enable --now qemu-guest-agent

Une VM démarrée avant l'installation du paquet doit être redémarrée : le canal
virtio de l'agent est branché au démarrage.`)
}

func (e *AgentError) Unwrap() error { return ErrAgentUnavailable }

// ExitCode implements the contract of PRD §7.5.
func (e *AgentError) ExitCode() int { return ExitGeneric }

// AgentInterfaces asks the guest agent what the guest's network looks like.
//
// GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces
func (c *Client) AgentInterfaces(ctx context.Context, node string, vmid int) ([]AgentInterface, error) {
	var out struct {
		Result []AgentInterface `json:"result"`
	}
	err := c.get(ctx, epQemuAgentIfaces, []string{node, strconv.Itoa(vmid)}, nil, &out)
	if err != nil {
		// PVE answers 500 for "no agent", "agent not running" and "guest is
		// off" alike. Translating it here is the difference between a message
		// that names the missing package and one that looks like an outage.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusInternalServerError {
			return nil, &AgentError{VMID: vmid, Err: err}
		}
		return nil, err
	}
	return out.Result, nil
}
