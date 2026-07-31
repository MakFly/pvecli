package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

func newVMAgentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Interroge l'agent QEMU d'une VM",
		Long: `Interroge l'agent invité.

PVE ne connaît pas l'adresse IP d'une VM en DHCP. L'hyperviseur voit une adresse
MAC sur un pont, et rien de plus : c'est l'invité qui sait dans quel réseau il
s'est inséré. L'agent est le seul canal par lequel il peut le dire.

C'est le lien entre l'hyperviseur et l'inventaire d'automatisation : sans agent,
« iac inventory » (PVX-042) ne peut pas savoir où se connecter.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newAgentIfacesCmd())
	return c
}

func newAgentIfacesCmd() *cobra.Command {
	var all bool

	c := &cobra.Command{
		Use:   "ifaces <vmid>",
		Short: "Liste les interfaces vues depuis l'invité",
		Long: `Affiche les interfaces réseau telles que l'invité les voit, avec leurs adresses.

L'interface de loopback est masquée par défaut : elle est toujours là et
n'apprend rien. --all la montre.

Endpoint : GET /api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			ifaces, err := client.AgentInterfaces(cmd.Context(), node, vmid)
			if err != nil {
				return err
			}

			kept := make([]pve.AgentInterface, 0, len(ifaces))
			for _, i := range ifaces {
				if !all && i.IsLoopback() {
					continue
				}
				kept = append(kept, i)
			}

			rows := output.Rows{Headers: []string{"INTERFACE", "MAC", "ADRESSES"}}
			for _, i := range kept {
				addrs := make([]string, 0, len(i.IPAddresses))
				for _, a := range i.IPAddresses {
					addrs = append(addrs, fmt.Sprintf("%s/%d", a.Address, a.Prefix))
				}
				rows.Cells = append(rows.Cells, []string{
					i.Name, firstNonEmpty(i.HardwareAddress, "—"), strings.Join(addrs, "  "),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, kept, rows)
		},
	}

	c.Flags().BoolVar(&all, "all", false, "montre aussi l'interface de loopback")
	addRenderFlags(c)
	return c
}

func newVMIPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ip <vmid>",
		Short: "Affiche la première IPv4 non-loopback de la VM",
		Long: `Affiche une seule adresse, sans en-tête ni décoration, pour être utilisable
directement dans un script :

    ssh debian@$(pvecli vm ip 212)

Si l'agent ne répond pas, la commande échoue avec le message qui explique quoi
installer — elle ne renvoie jamais une chaîne vide qu'un script prendrait pour
une adresse.

Endpoint : GET /api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			ifaces, err := client.AgentInterfaces(cmd.Context(), node, vmid)
			if err != nil {
				return err
			}
			for _, i := range ifaces {
				if ip := i.FirstIPv4(); ip != "" {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), ip)
					return nil
				}
			}
			return fmt.Errorf("la VM %d n'expose aucune adresse IPv4 non-loopback — l'agent répond mais l'invité n'a pas d'adresse", vmid)
		},
	}
}
