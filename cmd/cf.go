package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MakFly/pvecli/internal/cf"
	"github.com/MakFly/pvecli/internal/config"
	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/secret"
)

// cfTokenRef is where `cf tunnel create` files a connector token, and where the
// cloudflared role is told to look for it.
func cfTokenRef(tunnel string) secret.Ref {
	return secret.Ref{Service: "pvecli-cloudflare", Account: "tunnel-" + tunnel}
}

func newCFCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "cf",
		Short: "Pilote les tunnels Cloudflare qui exposent le lab",
		Long: `Crée les tunnels, tient leur table de routage et pose les CNAME.

Le modèle est SORTANT : cloudflared, dans la VM, ouvre une connexion vers le
réseau Cloudflare et le trafic redescend par ce tuyau. Aucun port n'est ouvert
sur la box, aucune adresse publique n'est nécessaire, et « pvecli cf » ne
configure jamais de redirection.

  pvecli cf tunnel create homelab
  pvecli cf route add n8n.exemple.tld --tunnel homelab --service http://192.168.1.220:5678
  pvecli vm declare app-01 --with cloudflared && pvecli iac apply

Le tunnel est créé en mode « remotely-managed » : sa table d'ingress vit chez
Cloudflare, pas dans un config.yml à l'intérieur de l'invité. Ajouter une
application est donc une ligne de plus ici, sans rien redéployer.

Identifiants :

  export CF_API_TOKEN="$(security find-generic-password -s pvecli-cloudflare -a api-token -w)"
  pvecli config set cf.account_id <identifiant du compte>

Le jeton n'est jamais accepté en argument : « ps » le rendrait visible à toute
la machine.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newCFStatusCmd(), newCFTunnelCmd(), newCFRouteCmd())
	return c
}

// newCFClient assembles a Cloudflare client from the environment and the config.
func newCFClient(cmd *cobra.Command) (*cf.Client, error) {
	eff, err := resolveConfig(cmd)
	if err != nil {
		return nil, err
	}
	return cf.New(cf.Options{
		Token:     os.Getenv(config.EnvCFToken),
		AccountID: eff.CF.AccountID,
	})
}

func newCFStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Vérifie le jeton et liste ce qu'il voit",
		Long: `Appelle /user/tokens/verify, puis liste les tunnels et les zones.

À lancer avant tout le reste : un jeton sans la permission « Cloudflare
Tunnel:Edit » ou « DNS:Edit » échoue plus tard, sur une création à moitié faite.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := client.Verify(ctx); err != nil {
				return err
			}
			errW := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(errW, "✓ jeton valide — compte %s\n", client.AccountID())

			tunnels, err := client.Tunnels(ctx)
			if err != nil {
				return err
			}
			zones, err := client.Zones(ctx)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(errW, "✓ %d tunnel(s), %d zone(s) visibles\n", len(tunnels), len(zones))

			rows := output.Rows{Headers: []string{"TYPE", "NOM", "IDENTIFIANT"}}
			for _, t := range tunnels {
				rows.Cells = append(rows.Cells, []string{"tunnel", t.Name, t.ID})
			}
			for _, z := range zones {
				rows.Cells = append(rows.Cells, []string{"zone", z.Name, z.ID})
			}
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts,
				map[string]any{"account": client.AccountID(), "tunnels": tunnels, "zones": zones}, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newCFTunnelCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "tunnel",
		Short: "Crée, liste et supprime les tunnels",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newCFTunnelCreateCmd(), newCFTunnelListCmd(), newCFTunnelRemoveCmd())
	return c
}

func newCFTunnelCreateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "create <nom>",
		Short: "Crée un tunnel et range son jeton de connecteur",
		Long: `Crée un tunnel « remotely-managed » et met son jeton de connecteur dans le
trousseau, sous « pvecli-cloudflare / tunnel-<nom> ».

Le jeton n'est PAS affiché. C'est lui, et lui seul, qui autorise une machine à
rejoindre ton compte Cloudflare : le rôle cloudflared le relit du trousseau au
moment de l'installation.

La table d'ingress créée ne contient que sa règle finale. Un tunnel sans route
est un tunnel qui démarre et répond 404 à tout — c'est l'état correct pour un
tunnel qui vient de naître.`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			errW := cmd.ErrOrStderr()

			// PRE-READ: a duplicate name is allowed by Cloudflare and becomes
			// permanently ambiguous for every command that resolves by name.
			if existing, err := client.TunnelByName(ctx, name); err == nil {
				return fmt.Errorf("un tunnel « %s » existe déjà (%s) — Cloudflare accepterait un homonyme,\n"+
					"mais toute commande qui résout par le nom deviendrait ambiguë", name, existing.ID)
			}

			_, _ = fmt.Fprintf(errW, "  requête  POST /accounts/%s/cfd_tunnel\n", client.AccountID())
			_, _ = fmt.Fprintf(errW, "  payload\n    name             %s\n    config_src       cloudflare\n", name)
			_, _ = fmt.Fprintf(errW, "  effet    un tunnel joignable, sans aucune route\n")

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été créé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(name, false); err != nil {
				return err
			}

			tunnel, err := client.CreateTunnel(ctx, name)
			if err != nil {
				return err
			}

			token, err := client.TunnelToken(ctx, tunnel.ID)
			if err != nil {
				return fmt.Errorf("tunnel %s créé, mais son jeton n'a pas pu être lu : %w", tunnel.ID, err)
			}
			ref := cfTokenRef(name)
			stored := "non conservé"
			if secretAvailable() {
				if err := secretStore(ref, token); err != nil {
					return err
				}
				stored = ref.String()
			} else {
				_, _ = fmt.Fprintf(errW,
					"\n⚠ aucun trousseau sur cette plateforme. Jeton de connecteur, affiché UNE fois :\n  %s\n", token)
			}

			// POST-READ: re-read from the API rather than echoing what we sent.
			back, err := client.TunnelByName(ctx, name)
			if err != nil {
				return fmt.Errorf("tunnel créé mais introuvable à la relecture : %w", err)
			}

			_, _ = fmt.Fprintf(errW, "\nEnsuite :\n  pvecli cf route add <fqdn> --tunnel %s --service http://<ip>:<port>\n", name)

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}}
			rows.Cells = append(rows.Cells,
				[]string{"nom", back.Name},
				[]string{"identifiant", back.ID},
				[]string{"cname", back.CNAME()},
				[]string{"jeton", stored},
			)
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts, back, rows)
		},
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newCFTunnelListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ls",
		Short: "Liste les tunnels du compte",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			tunnels, err := client.Tunnels(cmd.Context())
			if err != nil {
				return err
			}
			rows := output.Rows{Headers: []string{"NOM", "IDENTIFIANT", "ÉTAT", "CNAME"}}
			for _, t := range tunnels {
				rows.Cells = append(rows.Cells, []string{t.Name, t.ID, t.Status, t.CNAME()})
			}
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts, tunnels, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newCFTunnelRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "rm <nom>",
		Short: "Supprime un tunnel",
		Long: `Supprime un tunnel.

Cloudflare refuse tant qu'un connecteur y est encore rattaché. Ce refus est
utile : il veut dire qu'une machine route encore du trafic par ce tunnel.
Désinstalle cloudflared dans l'invité d'abord.

Les CNAME qui pointaient dessus ne sont PAS supprimés : ils appartiennent à ta
zone, pas au tunnel, et les effacer d'office ferait disparaître des noms que
d'autres choses utilisent peut-être. « pvecli cf route rm » s'en occupe.`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			tunnel, err := client.TunnelByName(ctx, name)
			if err != nil {
				return err
			}

			errW := cmd.ErrOrStderr()
			cfg, err := client.TunnelConfig(ctx, tunnel.ID)
			if err != nil {
				return err
			}
			routed := 0
			for _, r := range cfg.Ingress {
				if !r.IsCatchAll() {
					routed++
				}
			}
			_, _ = fmt.Fprintf(errW, "  requête  DELETE /accounts/%s/cfd_tunnel/%s\n", client.AccountID(), tunnel.ID)
			_, _ = fmt.Fprintf(errW, "  effet    %d route(s) cessent de répondre\n", routed)
			_, _ = fmt.Fprintf(errW, "  retour   aucun — un tunnel supprimé se recrée, avec un NOUVEL identifiant,\n"+
				"           ce qui invalide tous les CNAME qui pointaient dessus\n")

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été supprimé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(name, true); err != nil {
				return err
			}
			if err := client.DeleteTunnel(ctx, tunnel.ID); err != nil {
				return err
			}

			// POST-READ.
			if _, err := client.TunnelByName(ctx, name); err == nil {
				return fmt.Errorf("« %s » est toujours présent après la suppression", name)
			}
			_, _ = fmt.Fprintf(errW, "supprimé — %s\n", tunnel.ID)
			return nil
		},
	}
	addWriteFlags(c)
	return c
}

func newCFRouteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "route",
		Short: "Tient la table d'ingress d'un tunnel",
		Long: `Une route associe un nom public à un service du LAN.

  pvecli cf route add n8n.exemple.tld --tunnel homelab --service http://192.168.1.220:5678

Deux choses se passent : la règle entre dans la table d'ingress du tunnel, et un
CNAME proxifié est posé dans la zone. Les deux sont nécessaires — une règle sans
CNAME n'est jamais atteinte, un CNAME sans règle tombe sur la règle finale et
répond 404.

La table est ORDONNÉE et lue de haut en bas. pvecli garantit que le catch-all
reste le dernier : placé ailleurs, il avale toutes les règles suivantes sans
rien signaler.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newCFRouteAddCmd(), newCFRouteListCmd(), newCFRouteRemoveCmd())
	return c
}

func newCFRouteAddCmd() *cobra.Command {
	var tunnelName, service string

	c := &cobra.Command{
		Use:   "add <fqdn>",
		Short: "Route un nom public vers un service du LAN",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fqdn := args[0]
			if tunnelName == "" {
				return &exitError{code: pve.ExitUsage, msg: "--tunnel est obligatoire"}
			}
			if service == "" {
				return &exitError{code: pve.ExitUsage,
					msg: "--service est obligatoire, ex. --service http://192.168.1.220:5678"}
			}
			if !strings.Contains(service, "://") {
				return &exitError{code: pve.ExitUsage,
					msg: fmt.Sprintf("--service %q : il faut un schéma, ex. http://192.168.1.220:5678", service)}
			}

			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			tunnel, err := client.TunnelByName(ctx, tunnelName)
			if err != nil {
				return err
			}
			// The zone is resolved before anything is written: a hostname in no
			// zone of this account cannot be routed, and finding that out after
			// the ingress rule is in place leaves a half-done change.
			zone, err := client.ZoneForHost(ctx, fqdn)
			if err != nil {
				return err
			}
			cfg, err := client.TunnelConfig(ctx, tunnel.ID)
			if err != nil {
				return err
			}

			errW := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(errW, "  tunnel   %s (%s)\n", tunnel.Name, tunnel.ID)
			_, _ = fmt.Fprintf(errW, "  zone     %s\n", zone.Name)
			_, _ = fmt.Fprintf(errW, "  ingress  %s → %s\n", fqdn, service)
			_, _ = fmt.Fprintf(errW, "  dns      %s CNAME %s (proxifié)\n", fqdn, tunnel.CNAME())

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été écrit.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(fqdn, false); err != nil {
				return err
			}

			cfg.AddRoute(fqdn, service)
			if err := client.SetTunnelConfig(ctx, tunnel.ID, cfg); err != nil {
				return err
			}
			record, err := client.PointAtTunnel(ctx, zone, fqdn, tunnel)
			if err != nil {
				return err
			}

			// POST-READ: the ingress table as Cloudflare now holds it.
			back, err := client.TunnelConfig(ctx, tunnel.ID)
			if err != nil {
				return err
			}
			found := false
			for _, r := range back.Ingress {
				if r.Hostname == fqdn {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("la règle pour %s est absente de la table relue", fqdn)
			}

			_, _ = fmt.Fprintf(errW, "\nhttps://%s répondra dès que cloudflared tourne dans l'invité.\n", fqdn)
			return renderIngress(cmd, back, record)
		},
	}
	c.Flags().StringVar(&tunnelName, "tunnel", "", "nom du tunnel")
	c.Flags().StringVar(&service, "service", "", "service du LAN, ex. http://192.168.1.220:5678")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newCFRouteListCmd() *cobra.Command {
	var tunnelName string

	c := &cobra.Command{
		Use:   "ls",
		Short: "Affiche la table d'ingress d'un tunnel",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tunnelName == "" {
				return &exitError{code: pve.ExitUsage, msg: "--tunnel est obligatoire"}
			}
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			tunnel, err := client.TunnelByName(cmd.Context(), tunnelName)
			if err != nil {
				return err
			}
			cfg, err := client.TunnelConfig(cmd.Context(), tunnel.ID)
			if err != nil {
				return err
			}
			return renderIngress(cmd, cfg, cf.Record{})
		},
	}
	c.Flags().StringVar(&tunnelName, "tunnel", "", "nom du tunnel")
	addRenderFlags(c)
	return c
}

func newCFRouteRemoveCmd() *cobra.Command {
	var tunnelName string
	var keepDNS bool

	c := &cobra.Command{
		Use:   "rm <fqdn>",
		Short: "Retire une route et son CNAME",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fqdn := args[0]
			if tunnelName == "" {
				return &exitError{code: pve.ExitUsage, msg: "--tunnel est obligatoire"}
			}
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			tunnel, err := client.TunnelByName(ctx, tunnelName)
			if err != nil {
				return err
			}
			cfg, err := client.TunnelConfig(ctx, tunnel.ID)
			if err != nil {
				return err
			}
			if !cfg.RemoveRoute(fqdn) {
				return fmt.Errorf("aucune règle pour « %s » dans le tunnel %s", fqdn, tunnelName)
			}

			errW := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(errW, "  tunnel   %s\n  effet    %s cesse d'être routé", tunnel.Name, fqdn)
			if keepDNS {
				_, _ = fmt.Fprintf(errW, " ; le CNAME est conservé\n")
			} else {
				_, _ = fmt.Fprintf(errW, " ; le CNAME est supprimé\n")
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été supprimé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(fqdn, true); err != nil {
				return err
			}

			if err := client.SetTunnelConfig(ctx, tunnel.ID, cfg); err != nil {
				return err
			}
			if !keepDNS {
				zone, err := client.ZoneForHost(ctx, fqdn)
				if err != nil {
					return err
				}
				record, err := client.RecordByName(ctx, zone.ID, fqdn)
				if err != nil {
					return err
				}
				// Only a CNAME into a tunnel is ours to remove.
				if record != nil && strings.HasSuffix(record.Content, "."+cf.TunnelDomain) {
					if err := client.DeleteRecord(ctx, zone.ID, record.ID); err != nil {
						return err
					}
				} else if record != nil {
					_, _ = fmt.Fprintf(errW,
						"⚠ %s pointe vers %s, pas vers un tunnel : conservé.\n", fqdn, record.Content)
				}
			}

			back, err := client.TunnelConfig(ctx, tunnel.ID)
			if err != nil {
				return err
			}
			return renderIngress(cmd, back, cf.Record{})
		},
	}
	c.Flags().StringVar(&tunnelName, "tunnel", "", "nom du tunnel")
	c.Flags().BoolVar(&keepDNS, "keep-dns", false, "ne supprime pas le CNAME")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func renderIngress(cmd *cobra.Command, cfg cf.Config, record cf.Record) error {
	rows := output.Rows{Headers: []string{"NOM PUBLIC", "SERVICE"}}
	for _, r := range cfg.Ingress {
		name := r.Hostname
		if r.IsCatchAll() {
			name = "(tout le reste)"
		}
		rows.Cells = append(rows.Cells, []string{name, r.Service})
	}
	opts, err := renderOptions(cmd)
	if err != nil {
		return err
	}
	data := any(cfg)
	if record.ID != "" {
		data = map[string]any{"ingress": cfg.Ingress, "dns": record}
	}
	return output.Render(cmd.OutOrStdout(), opts, data, rows)
}
