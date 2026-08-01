package pve

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// FwOptions est le sous-ensemble des options de firewall d'un guest qui nous
// intéresse : est-il actif, et quelle est sa politique par défaut en entrée.
type FwOptions struct {
	Enable    int    `json:"enable"`
	PolicyIn  string `json:"policy_in"`
	PolicyOut string `json:"policy_out"`
}

// FwRule est une règle de firewall, telle que PVE la rend et l'accepte.
type FwRule struct {
	Pos     int    `json:"pos"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Enable  int    `json:"enable"`
	Proto   string `json:"proto"`
	Dport   string `json:"dport"`
	Source  string `json:"source"`
	Dest    string `json:"dest"`
	Comment string `json:"comment"`
}

// IPSet est un ensemble d'IP réutilisable, déclaré au niveau datacenter.
type IPSet struct {
	Name    string `json:"name"`
	Comment string `json:"comment"`
}

// IPSetEntry est une entrée d'un IPSet — une IP ou un CIDR.
type IPSetEntry struct {
	CIDR    string `json:"cidr"`
	Comment string `json:"comment"`
	Nomatch int    `json:"nomatch"`
}

// ClusterFirewallEnabled dit si le firewall datacenter est actif. C'est la
// bascule maîtresse : tant qu'elle est à 0, AUCUNE règle de guest ne filtre,
// et « lxc firewall enable » ne protège rien. On la lit pour pouvoir avertir.
func (c *Client) ClusterFirewallEnabled(ctx context.Context) (bool, error) {
	var o struct {
		Enable int `json:"enable"`
	}
	if err := c.get(ctx, epClusterFwOptions, nil, nil, &o); err != nil {
		return false, err
	}
	return o.Enable != 0, nil
}

// LXCFwOptions lit les options de firewall du conteneur.
func (c *Client) LXCFwOptions(ctx context.Context, node string, vmid int) (*FwOptions, error) {
	var o FwOptions
	if err := c.get(ctx, epLXCFwOptions, []string{node, strconv.Itoa(vmid)}, nil, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// SetLXCFwOptions écrit les options passées (enable, policy_in, …).
func (c *Client) SetLXCFwOptions(ctx context.Context, node string, vmid int, v url.Values) error {
	return c.post(ctx, epLXCFwOptionsSet, []string{node, strconv.Itoa(vmid)}, v, nil)
}

// LXCFwRules liste les règles du conteneur, dans l'ordre où elles s'appliquent.
func (c *Client) LXCFwRules(ctx context.Context, node string, vmid int) ([]FwRule, error) {
	var r []FwRule
	if err := c.get(ctx, epLXCFwRules, []string{node, strconv.Itoa(vmid)}, nil, &r); err != nil {
		return nil, err
	}
	return r, nil
}

// AddLXCFwRule ajoute une règle. Les champs vont dans v (type, action, proto…).
func (c *Client) AddLXCFwRule(ctx context.Context, node string, vmid int, v url.Values) error {
	return c.post(ctx, epLXCFwRuleCreate, []string{node, strconv.Itoa(vmid)}, v, nil)
}

// DeleteLXCFwRule retire la règle à la position pos.
func (c *Client) DeleteLXCFwRule(ctx context.Context, node string, vmid, pos int) error {
	return c.del(ctx, epLXCFwRuleDelete, []string{node, strconv.Itoa(vmid), strconv.Itoa(pos)}, nil, nil)
}

// SetLXCNICFirewall pose (ou retire) « firewall=1 » sur net0. Sans ce drapeau
// sur l'interface, les règles du guest ne s'appliquent tout simplement pas :
// c'est le point qu'on oublie et qui fait croire à un firewall inopérant.
func (c *Client) SetLXCNICFirewall(ctx context.Context, node string, vmid int, on bool) (bool, error) {
	var cfg map[string]any
	if err := c.get(ctx, epLXCConfig, []string{node, strconv.Itoa(vmid)}, nil, &cfg); err != nil {
		return false, err
	}
	net0, _ := cfg["net0"].(string)
	if net0 == "" {
		return false, fmt.Errorf("le conteneur %d n'a pas d'interface net0", vmid)
	}
	updated := withFirewallFlag(net0, on)
	if updated == net0 {
		return false, nil
	}
	v := url.Values{}
	v.Set("net0", updated)
	if _, err := c.UpdateGuestConfig(ctx, node, TypeLXC, vmid, v); err != nil {
		return false, err
	}
	return true, nil
}

// withFirewallFlag renvoie l'option net0 avec firewall=0/1, en remplaçant le
// drapeau s'il existe déjà et en le préservant sinon — sans toucher au reste
// (bridge, ip, gw…).
func withFirewallFlag(net string, on bool) string {
	want := "firewall=1"
	if !on {
		want = "firewall=0"
	}
	parts := strings.Split(net, ",")
	for i, p := range parts {
		if strings.HasPrefix(p, "firewall=") {
			parts[i] = want
			return strings.Join(parts, ",")
		}
	}
	return strings.Join(append(parts, want), ",")
}

// IPSets liste les ensembles d'IP du datacenter.
func (c *Client) IPSets(ctx context.Context) ([]IPSet, error) {
	var s []IPSet
	if err := c.get(ctx, epClusterIPSets, nil, nil, &s); err != nil {
		return nil, err
	}
	return s, nil
}

// CreateIPSet crée un ensemble d'IP (idempotent côté appelant : PVE refuse un
// doublon, à l'appelant de l'ignorer s'il le souhaite).
func (c *Client) CreateIPSet(ctx context.Context, name, comment string) error {
	v := url.Values{}
	v.Set("name", name)
	if comment != "" {
		v.Set("comment", comment)
	}
	return c.post(ctx, epClusterIPSetNew, nil, v, nil)
}

// IPSetEntries liste les entrées d'un ensemble.
func (c *Client) IPSetEntries(ctx context.Context, name string) ([]IPSetEntry, error) {
	var e []IPSetEntry
	if err := c.get(ctx, epClusterIPSet, []string{name}, nil, &e); err != nil {
		return nil, err
	}
	return e, nil
}

// AddIPSetEntry ajoute une IP ou un CIDR à l'ensemble.
func (c *Client) AddIPSetEntry(ctx context.Context, name, cidr, comment string) error {
	v := url.Values{}
	v.Set("cidr", cidr)
	if comment != "" {
		v.Set("comment", comment)
	}
	return c.post(ctx, epClusterIPSetAdd, []string{name}, v, nil)
}

// DelIPSetEntry retire une entrée de l'ensemble.
func (c *Client) DelIPSetEntry(ctx context.Context, name, cidr string) error {
	return c.del(ctx, epClusterIPSetDel, []string{name, cidr}, nil, nil)
}
