package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

// ── Exécution dans l'invité ──────────────────────────────────────────────────
//
// Pourquoi c'est ici et pas dans un script SSH : administrer une VM par SSH
// suppose un compte, une clé déposée, un port ouvert et un réseau qui marche.
// Quatre choses qui peuvent manquer — et qui manquent précisément le jour où
// l'on a besoin d'entrer. L'agent invité passe par l'hyperviseur : il ne
// dépend ni du réseau de la VM, ni de sshd, ni d'une clé. C'est le canal qui
// reste quand celui qu'on préfère est tombé.
//
// Deux limites du canal, qui ne sont pas négociables et qu'il vaut mieux
// connaître avant de s'en servir :
//
//   - il n'y a PAS de shell. `agent exec` lance un exécutable avec des
//     arguments ; « cd /x && y | z » ne veut rien dire pour lui. Pour une
//     ligne de shell, il faut la donner à un shell : sh -c "…".
//   - la sortie est bufferisée par l'agent et rendue à la fin. Ce n'est pas un
//     terminal : rien ne défile, on attend, puis on lit tout.

// flexBool accepte aussi bien un booléen JSON qu'un nombre 0/1. Selon la
// version, PVE renvoie « out-truncated » tantôt en true/false, tantôt en 1/0 :
// PVE 9.2 le sérialise en entier, ce qui casse un décodage en bool strict.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	switch s {
	case "true", "1", "\"1\"":
		*b = true
	case "false", "0", "\"0\"", "null", "":
		*b = false
	default:
		// Tout nombre non nul est vrai ; à défaut, on tente le bool natif.
		var n float64
		if err := json.Unmarshal(data, &n); err == nil {
			*b = n != 0
			return nil
		}
		var raw bool
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*b = flexBool(raw)
	}
	return nil
}

// AgentExecResult is what the guest reports once the command has finished.
type AgentExecResult struct {
	Exited   int    `json:"exited"`
	ExitCode int    `json:"exitcode"`
	OutData  string `json:"out-data"`
	ErrData  string `json:"err-data"`
	// PVE dit « truncated » quand la sortie a dépassé ce que l'agent garde.
	OutTruncated flexBool `json:"out-truncated"`
	ErrTruncated flexBool `json:"err-truncated"`
}

// AgentExec runs argv inside the guest and waits for it to finish.
//
// `poll` is how often the result is asked for. The call returns as soon as the
// guest says the process exited, or when ctx is done — a build that takes ten
// minutes is normal here, so the caller's timeout is what bounds this, not an
// arbitrary constant baked in.
func (c *Client) AgentExec(ctx context.Context, node string, vmid int, argv []string, poll time.Duration) (*AgentExecResult, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("aucune commande à exécuter")
	}

	body := url.Values{}
	// PVE attend « command » répété, un élément par argument. Une seule chaîne
	// serait comprise comme un exécutable dont le nom contient des espaces.
	for _, a := range argv {
		body.Add("command", a)
	}

	var started struct {
		PID int `json:"pid"`
	}
	if err := c.post(ctx, epQemuAgentExec, []string{node, strconv.Itoa(vmid)}, body, &started); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusInternalServerError {
			return nil, &AgentError{VMID: vmid, Err: err}
		}
		return nil, err
	}
	if started.PID == 0 {
		return nil, fmt.Errorf("l'agent n'a pas rendu de PID — la commande n'a pas démarré")
	}

	if poll <= 0 {
		poll = time.Second
	}
	q := url.Values{"pid": {strconv.Itoa(started.PID)}}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	// Le PID est connu et la commande tourne : c'est vrai quel que soit
	// l'endroit où le délai nous rattrape. Une fonction, pour que les deux
	// sorties possibles disent exactement la même chose.
	stillRunning := func(cause error) error {
		return fmt.Errorf("commande toujours en cours dans la VM %d (pid %d) — "+
			"délai dépassé côté client, pas côté invité : %w", vmid, started.PID, cause)
	}

	for {
		var res AgentExecResult
		if err := c.get(ctx, epQemuAgentStatus, []string{node, strconv.Itoa(vmid)}, q, &res); err != nil {
			// Le délai peut expirer PENDANT la requête aussi bien qu'entre
			// deux. Rendre l'erreur de transport telle quelle perdrait le PID
			// dans ce cas-là — et laquelle des deux fenêtres attrape le
			// dépassement est une course, donc le message changeait d'une
			// exécution à l'autre.
			if ctx.Err() != nil {
				return nil, stillRunning(ctx.Err())
			}
			return nil, err
		}
		if res.Exited != 0 {
			return &res, nil
		}
		select {
		case <-ctx.Done():
			// On rend le PID : la commande TOURNE toujours dans l'invité, et
			// abandonner sans le dire laisserait quelqu'un croire qu'elle est
			// morte avec nous.
			return nil, stillRunning(ctx.Err())
		case <-ticker.C:
		}
	}
}
