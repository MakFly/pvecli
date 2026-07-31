package cmd

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/MakFly/pvectl/internal/output"
	"github.com/MakFly/pvectl/internal/pve"
	"github.com/MakFly/pvectl/internal/service"
	"github.com/spf13/cobra"
)

func newVMSetCmd() *cobra.Command {
	var (
		cores    int
		memory   int
		name     string
		tags     string
		ciUser   string
		sshKeys  string
		ipConfig string
		raw      []string
	)

	c := &cobra.Command{
		Use:   "set <vmid>",
		Short: "Modifie la configuration d'une VM (PUT /nodes/{node}/qemu/{vmid}/config)",
		Long: `Modifie la configuration d'une VM.

Seules les clés explicitement passées sont envoyées : une écriture demandée
n'est jamais élargie (PRD §7.6). --set permet d'écrire n'importe quelle clé de
l'API directement, sous la forme « clé=valeur ».

Les clés cloud-init ne prennent effet qu'au prochain démarrage : cloud-init lit
son disque au boot. Un « set » sur une VM qui tourne modifie la configuration,
pas le système invité.
` + ownershipHelp + `

Endpoint : PUT /api2/json/nodes/{node}/qemu/{vmid}/config`,
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
			owner, err := newOwnership(cmd)
			if err != nil {
				return err
			}

			params := url.Values{}
			if cores > 0 {
				params.Set("cores", strconv.Itoa(cores))
			}
			if memory > 0 {
				params.Set("memory", strconv.Itoa(memory))
			}
			if name != "" {
				params.Set("name", name)
			}
			if tags != "" {
				params.Set("tags", strings.ReplaceAll(tags, ",", ";"))
			}
			if ciUser != "" {
				params.Set("ciuser", ciUser)
			}
			if ipConfig != "" {
				params.Set("ipconfig0", ipConfig)
			}
			if sshKeys != "" {
				keys, err := readSSHKeys(sshKeys)
				if err != nil {
					return err
				}
				params.Set("sshkeys", keys)
			}
			for _, kv := range raw {
				key, value, ok := strings.Cut(kv, "=")
				if !ok {
					return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("--set attend clé=valeur, reçu %q", kv)}
				}
				params.Set(key, value)
			}

			if len(params) == 0 {
				return &exitError{code: pve.ExitUsage, msg: "aucune modification demandée"}
			}

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "PUT",
					Path:     pve.ConfigPath(pve.TypeQEMU, node, vmid),
					Payload:  params,
					Effect:   fmt.Sprintf("configuration de la VM %d modifiée", vmid),
					Rollback: "réécrire les anciennes valeurs",
					Verify:   fmt.Sprintf("relecture de la configuration de %d", vmid),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, pve.TypeQEMU, vmid)
					if err != nil {
						return service.State{}, err
					}
					if err := owner.check(vmid, st.Tags, opSetConfig); err != nil {
						return service.State{}, err
					}
					return guestState(st), nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.UpdateGuestConfig(ctx, node, pve.TypeQEMU, vmid, params)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeQEMU, vmid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "configurée", Raw: cfg}, nil
				},
			})
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CLÉ", "VALEUR"}}
			for _, k := range sortedKeys(params) {
				rows.Cells = append(rows.Cells, []string{k, params.Get(k)})
			}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.IntVar(&cores, "cores", 0, "nombre de cœurs")
	f.IntVar(&memory, "memory", 0, "mémoire en Mio")
	f.StringVar(&name, "name", "", "nom de la VM")
	f.StringVar(&tags, "tags", "", "tags, séparés par des virgules")
	f.StringVar(&ciUser, "ci-user", "", "utilisateur cloud-init")
	f.StringVar(&sshKeys, "ssh-keys", "", "fichier de clés publiques SSH")
	f.StringVar(&ipConfig, "ipconfig0", "", "configuration réseau cloud-init, ex. ip=192.0.2.211/24,gw=192.0.2.1")
	f.StringArrayVar(&raw, "set", nil, "clé=valeur brute de l'API, répétable")
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func sortedKeys(v url.Values) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
