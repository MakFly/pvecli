package cmd

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dev-toolings/pvecli/internal/pve"
)

// newLXCFirewallCmd regroupe le pilotage du firewall PVE d'un conteneur. C'est
// la best practice Proxmox : filtrer à l'hyperviseur, par-guest, via l'API —
// pas du nftables posé à la main dans l'invité.
func newLXCFirewallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "firewall",
		Short: "Pilote le firewall PVE d'un conteneur",
		Long: `Pilote le firewall PVE d'un conteneur — le filtrage vit à l'hyperviseur.

Rappel de best practice : une règle de guest ne filtre QUE si (1) le firewall
datacenter est actif, et (2) l'interface porte « firewall=1 ». « enable » pose
le second ; le premier est une bascule globale — délibérément PAS automatisée
ici, car l'activer sur un nœud qu'on ne joint que par l'API peut couper l'accès.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(
		newFwShowCmd(), newFwEnableCmd(), newFwDisableCmd(),
		newFwAllowCmd(), newFwRuleRmCmd(),
	)
	return c
}

func newFwShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid>",
		Short: "Affiche l'état du firewall du conteneur",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, node, client, err := fwTarget(cmd, args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			dc, err := client.ClusterFirewallEnabled(ctx)
			if err != nil {
				return err
			}
			opts, err := client.LXCFwOptions(ctx, node, vmid)
			if err != nil {
				return err
			}
			rules, err := client.LXCFwRules(ctx, node, vmid)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(out, "firewall datacenter : %s\n", onOff(dc))
			if !dc {
				_, _ = fmt.Fprintln(out, "  ⚠ tant qu'il est inactif, AUCUNE règle ci-dessous ne filtre.")
			}
			_, _ = fmt.Fprintf(out, "firewall du guest   : %s (policy_in=%s policy_out=%s)\n",
				onOff(opts.Enable != 0), orDash(opts.PolicyIn), orDash(opts.PolicyOut))
			if len(rules) == 0 {
				_, _ = fmt.Fprintln(out, "règles              : (aucune)")
				return nil
			}
			_, _ = fmt.Fprintln(out, "règles :")
			for _, r := range rules {
				_, _ = fmt.Fprintf(out, "  [%d] %s %s proto=%s dport=%s source=%s %s\n",
					r.Pos, r.Type, r.Action, orDash(r.Proto), orDash(r.Dport), orDash(r.Source), r.Comment)
			}
			return nil
		},
	}
}

func newFwEnableCmd() *cobra.Command {
	var policyIn string
	c := &cobra.Command{
		Use:   "enable <vmid>",
		Short: "Active le firewall du guest : NIC firewall=1 + politique par défaut",
		Long: `Active le firewall du conteneur selon la best practice deny-in :
  - pose « firewall=1 » sur net0 (sans quoi rien ne filtre) ;
  - met le firewall du guest à enable=1, policy_in=DROP (par défaut), policy_out=ACCEPT.

Ensuite, ouvre ce qu'il faut avec « lxc firewall allow ». Si le firewall
datacenter est inactif, la commande le signale : il faudra l'activer pour que
tout ceci prenne effet.`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, node, client, err := fwTarget(cmd, args)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			changed, err := client.SetLXCNICFirewall(ctx, node, vmid, true)
			if err != nil {
				return err
			}
			v := url.Values{}
			v.Set("enable", "1")
			v.Set("policy_in", policyIn)
			v.Set("policy_out", "ACCEPT")
			if err := client.SetLXCFwOptions(ctx, node, vmid, v); err != nil {
				return err
			}

			if changed {
				_, _ = fmt.Fprintln(out, "net0 : firewall=1 posé")
			}
			_, _ = fmt.Fprintf(out, "firewall du guest %d : actif, policy_in=%s\n", vmid, policyIn)

			dc, err := client.ClusterFirewallEnabled(ctx)
			if err == nil && !dc {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"⚠ le firewall DATACENTER est inactif — rien ne filtre tant qu'il n'est pas activé.\n"+
						"  active-le en conscience (il peut couper l'accès au nœud) : Datacenter → Firewall → Options.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&policyIn, "policy-in", "DROP", "politique par défaut en entrée : DROP, REJECT ou ACCEPT")
	return c
}

func newFwDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <vmid>",
		Short: "Désactive le firewall du guest (les règles restent, inertes)",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, node, client, err := fwTarget(cmd, args)
			if err != nil {
				return err
			}
			v := url.Values{}
			v.Set("enable", "0")
			if err := client.SetLXCFwOptions(cmd.Context(), node, vmid, v); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "firewall du guest %d : désactivé\n", vmid)
			return nil
		},
	}
}

func newFwAllowCmd() *cobra.Command {
	var proto, dport, source, comment string
	c := &cobra.Command{
		Use:   "allow <vmid>",
		Short: "Ajoute une règle ACCEPT entrante",
		Long: `Ajoute une règle ACCEPT en entrée.

  pvecli lxc firewall allow 221 --dport 5432 --source 192.168.1.220
  pvecli lxc firewall allow 221 --dport 7700 --source +dc/infra_clients

--source accepte une IP, un CIDR, ou « +nom » pour un IPSet (voir « pvecli fw ipset »).`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dport == "" {
				return &exitError{code: pve.ExitUsage, msg: "--dport est requis"}
			}
			vmid, node, client, err := fwTarget(cmd, args)
			if err != nil {
				return err
			}
			v := url.Values{}
			v.Set("type", "in")
			v.Set("action", "ACCEPT")
			v.Set("enable", "1")
			v.Set("proto", proto)
			v.Set("dport", dport)
			if source != "" {
				v.Set("source", source)
			}
			if comment == "" {
				comment = "pvecli lxc firewall allow"
			}
			v.Set("comment", comment)
			if err := client.AddLXCFwRule(cmd.Context(), node, vmid, v); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "règle ajoutée : ACCEPT %s/%s depuis %s\n", proto, dport, orDash(source))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&proto, "proto", "tcp", "protocole : tcp ou udp")
	f.StringVar(&dport, "dport", "", "port de destination (requis)")
	f.StringVar(&source, "source", "", "IP, CIDR, ou +ipset autorisé")
	f.StringVar(&comment, "comment", "", "commentaire de la règle")
	return c
}

func newFwRuleRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <vmid> <pos>",
		Short: "Supprime la règle à la position donnée",
		Args:  usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, err := strconv.Atoi(args[1])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("position invalide : %q", args[1])}
			}
			vmid, node, client, err := fwTarget(cmd, args)
			if err != nil {
				return err
			}
			if err := client.DeleteLXCFwRule(cmd.Context(), node, vmid, pos); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "règle %d supprimée\n", pos)
			return nil
		},
	}
}

// newFwCmd est le pilotage des IPSets datacenter — des ensembles d'IP
// réutilisables qu'une règle référence par « +nom ».
func newFwCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "fw",
		Short: "Firewall datacenter : IPSets réutilisables",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newIPSetCmd())
	return c
}

func newIPSetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ipset",
		Short: "Ensembles d'IP réutilisables (niveau datacenter)",
		Args:  usage(cobra.NoArgs),
	}

	c.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "Liste les IPSets",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			sets, err := client.IPSets(cmd.Context())
			if err != nil {
				return err
			}
			for _, s := range sets {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s %s\n", s.Name, s.Comment)
			}
			return nil
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "create <name> [comment]",
		Short: "Crée un IPSet",
		Args:  usage(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			comment := ""
			if len(args) == 2 {
				comment = args[1]
			}
			if err := client.CreateIPSet(cmd.Context(), args[0], comment); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ipset %q créé\n", args[0])
			return nil
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Liste les entrées d'un IPSet",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			entries, err := client.IPSetEntries(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, e := range entries {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s %s\n", e.CIDR, e.Comment)
			}
			return nil
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "add <name> <cidr> [comment]",
		Short: "Ajoute une IP/CIDR à un IPSet",
		Args:  usage(cobra.RangeArgs(2, 3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			comment := ""
			if len(args) == 3 {
				comment = args[2]
			}
			if err := client.AddIPSetEntry(cmd.Context(), args[0], args[1], comment); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s ajouté à %q\n", args[1], args[0])
			return nil
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "del <name> <cidr>",
		Short: "Retire une IP/CIDR d'un IPSet",
		Args:  usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := client.DelIPSetEntry(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s retiré de %q\n", args[1], args[0])
			return nil
		},
	})

	return c
}

// fwTarget résout vmid + nœud + client, communs à toutes les sous-commandes.
func fwTarget(cmd *cobra.Command, args []string) (int, string, *pve.Client, error) {
	vmid, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, "", nil, &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
	}
	client, err := newClient(cmd)
	if err != nil {
		return 0, "", nil, err
	}
	node, err := targetNode(cmd, nil)
	if err != nil {
		return 0, "", nil, err
	}
	return vmid, node, client, nil
}

func onOff(b bool) string {
	if b {
		return "actif"
	}
	return "inactif"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
