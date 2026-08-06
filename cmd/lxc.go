package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/service"
	"github.com/spf13/cobra"
)

// EnvCTPassword is the one place a container's root password may come from
// besides stdin. A password given as a command-line argument is visible in
// `ps`, in the shell history and in every process listing on the machine — so
// --password takes no value here, it only asks where to read one.
const EnvCTPassword = "PVECLI_CT_PASSWORD"

func newLXCCreateCmd() *cobra.Command {
	var (
		o             pve.CTOptions
		sshKeysFile   string
		passwordStdin bool
		start         bool
	)

	c := &cobra.Command{
		Use:   "create <vmid>",
		Short: "Crée un conteneur LXC (POST /nodes/{node}/lxc)",
		Long: `Crée un conteneur LXC à partir d'un template de système (vztmpl).

NON PRIVILÉGIÉ PAR DÉFAUT. C'est le sujet du chapitre 03, et ce n'est pas une
préférence de style : dans un conteneur non privilégié, les UID sont décalés
vers une plage inutilisée de l'hôte — root dans le conteneur est l'uid 100000
au dehors. Un processus qui s'échappe se retrouve donc sur l'hôte en tant
qu'utilisateur qui ne possède rien. Dans un conteneur privilégié, root dedans
est root dehors : la frontière du conteneur devient la seule chose qui sépare
les deux. --privileged existe, et devrait rester inutilisé.

MOT DE PASSE. Il n'est jamais accepté en argument : la ligne de commande est
lisible par « ps » et reste dans l'historique du shell. Deux voies :

    pvecli lxc create 120 … --password-stdin < motdepasse.txt
    PVECLI_CT_PASSWORD=… pvecli lxc create 120 …

Sans mot de passe ni clé SSH, le conteneur démarre sans aucun accès — ce qui
est un état parfaitement valide pour un conteneur piloté par la console du
nœud.

Le template doit exister sur le storage indiqué :

    pvecli storage content local --content vztmpl

Endpoint : POST /api2/json/nodes/{node}/lxc`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			if o.OSTemplate == "" {
				return &exitError{code: pve.ExitUsage, msg: "--ostemplate est obligatoire (pvecli storage content local --content vztmpl)"}
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

			if sshKeysFile != "" {
				raw, err := os.ReadFile(sshKeysFile)
				if err != nil {
					return fmt.Errorf("lecture des clés SSH : %w", err)
				}
				// Unlike QEMU's `sshkeys`, the LXC endpoint takes the keys
				// verbatim, one per line: no URL encoding on top.
				o.SSHKeys = strings.TrimSpace(string(raw))
			}
			if passwordStdin {
				pwd, err := readPasswordFromStdin(cmd.InOrStdin())
				if err != nil {
					return err
				}
				o.Password = pwd
			} else if env := os.Getenv(EnvCTPassword); env != "" {
				o.Password = env
			}

			params := o.Values()
			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.CreatePath(pve.TypeLXC, node),
					Payload:  params,
					Effect:   fmt.Sprintf("création du conteneur %d sur %s (%s)", vmid, node, privilegeLabel(o.Privileged)),
					Rollback: fmt.Sprintf("pvecli lxc rm %d", vmid),
					Verify:   fmt.Sprintf("relecture de la configuration de %d", vmid),
				},
				// Two conditions before any write: the vmid must be free, and
				// the template must actually be on the storage it names.
				PreRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.GuestStatus(ctx, node, pve.TypeLXC, vmid); err == nil {
						return service.State{}, fmt.Errorf("le vmid %d est déjà pris — choisis-en un autre ou supprime l'existant", vmid)
					}
					if err := checkTemplateExists(ctx, client, node, o.OSTemplate); err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.CreateGuest(ctx, node, pve.TypeLXC, vmid, params)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeLXC, vmid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{
						Exists:  true,
						Status:  "created",
						Summary: fmt.Sprintf("conteneur %d créé", vmid),
						Raw:     cfg,
					}, nil
				},
			})
			if err != nil {
				return err
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun && start {
				if _, err := client.SetGuestStatus(cmd.Context(), node, pve.TypeLXC, vmid, pve.ActionStart, nil); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "démarrage demandé — suis-le avec « pvecli task ls --running »\n")
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"vmid", target}, {"nœud", node},
				{"privilèges", privilegeLabel(o.Privileged)}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.StringVar(&o.Hostname, "hostname", "", "nom d'hôte du conteneur")
	f.StringVar(&o.OSTemplate, "ostemplate", "", "volid du template système, ex. local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst")
	f.IntVar(&o.Cores, "cores", 1, "nombre de cœurs")
	f.IntVar(&o.Memory, "memory", 512, "mémoire en Mio")
	f.IntVar(&o.Swap, "swap", 0, "swap en Mio (0 : laisse le défaut du nœud)")
	f.StringVar(&o.RootFS, "rootfs", "local-lvm:8", "volume racine, sous la forme storage:taille_en_Gio")
	f.StringVar(&o.Bridge, "net", "vmbr0", "pont réseau ; devient net0=name=eth0,bridge=…")
	f.StringVar(&o.IP, "ip", "dhcp", "adresse IP : dhcp, ou 192.0.2.120/24")
	f.StringVar(&o.Gateway, "gateway", "", "passerelle, si --ip est statique")
	f.StringVar(&o.Nameserver, "nameserver", "", "serveur DNS (défaut : celui de l'hôte)")
	f.StringVar(&o.OSType, "ostype", "", "type d'OS du template (debian, ubuntu, alpine…)")
	f.StringVar(&o.Tags, "tags", "", "tags, séparés par des virgules")
	f.BoolVar(&o.OnBoot, "onboot", false, "démarre le conteneur au démarrage du nœud")
	f.BoolVar(&o.Privileged, "privileged", false, "conteneur PRIVILÉGIÉ : root dedans = root sur l'hôte — déconseillé")
	f.StringVar(&sshKeysFile, "ssh-keys", "", "fichier de clés publiques SSH à injecter")
	f.BoolVar(&passwordStdin, "password-stdin", false, "lit le mot de passe root sur l'entrée standard")
	f.BoolVar(&start, "start", false, "démarre le conteneur après création")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func privilegeLabel(privileged bool) string {
	if privileged {
		return "privilégié"
	}
	return "non privilégié"
}

// readPasswordFromStdin takes the whole of stdin as the password, trailing
// newline removed — the shape `… --password-stdin < fichier` produces.
func readPasswordFromStdin(in io.Reader) (string, error) {
	raw, err := io.ReadAll(bufio.NewReader(in))
	if err != nil {
		return "", fmt.Errorf("lecture du mot de passe sur l'entrée standard : %w", err)
	}
	pwd := strings.TrimRight(string(raw), "\r\n")
	if pwd == "" {
		return "", &exitError{code: pve.ExitUsage, msg: "--password-stdin : rien à lire sur l'entrée standard"}
	}
	return pwd, nil
}

// checkTemplateExists turns "the node refused your create" into "this template
// is not there, here is what is".
func checkTemplateExists(ctx context.Context, client *pve.Client, node, volid string) error {
	storage := pve.TemplateStorage(volid)
	if storage == "" {
		return fmt.Errorf("--ostemplate attend un volid « storage:vztmpl/fichier », reçu %q", volid)
	}

	volumes, err := client.StorageContent(ctx, node, storage, "vztmpl")
	if err != nil {
		return fmt.Errorf("lecture des templates du storage %q : %w", storage, err)
	}
	available := make([]string, 0, len(volumes))
	for _, v := range volumes {
		if v.VolID == volid {
			return nil
		}
		available = append(available, v.VolID)
	}

	msg := fmt.Sprintf("le template %q est absent du storage %q", volid, storage)
	if len(available) == 0 {
		return fmt.Errorf("%s — ce storage n'en contient aucun.\n"+
			"  dépose-en un depuis le nœud :  pveam available --section system && pveam download %s <template>", msg, storage)
	}
	return fmt.Errorf("%s. Disponibles :\n  %s", msg, strings.Join(available, "\n  "))
}

func newLXCSetCmd() *cobra.Command {
	var (
		cores       int
		memory      int
		swap        int
		hostname    string
		description string
		tags        string
		nameserver  string
		onboot      string
		remove      []string
		raw         []string
	)

	c := &cobra.Command{
		Use:   "set <vmid>",
		Short: "Modifie la configuration d'un conteneur (PUT /nodes/{node}/lxc/{vmid}/config)",
		Long: `Modifie la configuration d'un conteneur LXC.

Seules les clés explicitement passées sont envoyées : une écriture demandée
n'est jamais élargie (PRD §7.6). --set écrit n'importe quelle clé de l'API sous
la forme « clé=valeur », et --delete en retire une.

« unprivileged » ne figure pas parmi les drapeaux : le basculer sur un
conteneur existant ne remappe pas les UID de son système de fichiers, et laisse
un conteneur qui ne démarre plus. Le choix se fait à la création.
` + ownershipHelp + `

Endpoint : PUT /api2/json/nodes/{node}/lxc/{vmid}/config`,
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

			params := url.Values{}
			if cores > 0 {
				params.Set("cores", strconv.Itoa(cores))
			}
			if memory > 0 {
				params.Set("memory", strconv.Itoa(memory))
			}
			if swap > 0 {
				params.Set("swap", strconv.Itoa(swap))
			}
			if hostname != "" {
				params.Set("hostname", hostname)
			}
			if description != "" {
				params.Set("description", description)
			}
			if tags != "" {
				params.Set("tags", strings.ReplaceAll(tags, ",", ";"))
			}
			if nameserver != "" {
				params.Set("nameserver", nameserver)
			}
			if onboot != "" {
				params.Set("onboot", onboot)
			}
			if len(remove) > 0 {
				params.Set("delete", strings.Join(remove, ","))
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

			owner, err := newOwnership(cmd)
			if err != nil {
				return err
			}

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "PUT",
					Path:     pve.ConfigPath(pve.TypeLXC, node, vmid),
					Payload:  params,
					Effect:   fmt.Sprintf("configuration du conteneur %d modifiée", vmid),
					Rollback: "réécrire les anciennes valeurs",
					Verify:   fmt.Sprintf("relecture de la configuration de %d", vmid),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, pve.TypeLXC, vmid)
					if err != nil {
						return service.State{}, err
					}
					if err := owner.check(vmid, st.Tags, opSetConfig); err != nil {
						return service.State{}, err
					}
					return guestState(st), nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.UpdateGuestConfig(ctx, node, pve.TypeLXC, vmid, params)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeLXC, vmid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "configuré", Raw: cfg}, nil
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
	f.IntVar(&swap, "swap", 0, "swap en Mio")
	f.StringVar(&hostname, "hostname", "", "nom d'hôte")
	f.StringVar(&description, "description", "", "description")
	f.StringVar(&tags, "tags", "", "tags, séparés par des virgules")
	f.StringVar(&nameserver, "nameserver", "", "serveur DNS")
	f.StringVar(&onboot, "onboot", "", "démarrage au boot du nœud : 1 ou 0")
	f.StringArrayVar(&remove, "delete", nil, "clé de configuration à supprimer, répétable")
	f.StringArrayVar(&raw, "set", nil, "clé=valeur brute de l'API, répétable")
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// ctCloneKind names what PVE actually did, not what was asked.
//
// Observed on the lab: cloning an ordinary container without --full still
// rsync'd 546 MB of files. PVE only makes a linked clone from a TEMPLATE;
// everywhere else the flag is ignored. Reporting "clone lié" there would be the
// same lie as printing "template = 1" under --dry-run.
func ctCloneKind(srcIsTemplate, full bool) string {
	if !srcIsTemplate {
		return "copie complète (la source n'est pas un template)"
	}
	return cloneKind(full)
}

func newLXCCloneCmd() *cobra.Command {
	var o pve.CloneOptions

	c := &cobra.Command{
		Use:   "clone <source-vmid>",
		Short: "Clone un conteneur (POST /nodes/{node}/lxc/{vmid}/clone)",
		Long: `Produit un nouveau conteneur à partir d'un conteneur existant.

  --full   copie indépendante des volumes.
  (défaut) PVE tente un clone LIÉ, mais seulement depuis un TEMPLATE : cloner
           un conteneur ordinaire produit toujours une copie complète, quoi
           qu'on demande. La différence avec QEMU vient du schéma, pas d'un
           choix de cette CLI.

Le nouveau nom passe par « hostname », pas par « name » comme côté QEMU.

Comme côté QEMU, la garde de propriété porte sur la SOURCE : le clone n'existe
pas encore, donc rien ne le possède.

Endpoint : POST /api2/json/nodes/{node}/lxc/{vmid}/clone`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid source invalide : %q", args[0])}
			}
			if o.NewID == 0 {
				return &exitError{code: pve.ExitUsage, msg: "--newid est obligatoire"}
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

			params := o.Values(pve.TypeLXC)
			target := strconv.Itoa(o.NewID)
			runner := newRunner(cmd, client)
			// Read by the pre-read, reported afterwards: what PVE actually did
			// depends on the source, not on what was asked.
			srcIsTemplate := false

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.ClonePath(pve.TypeLXC, node, src),
					Payload:  params,
					Effect:   fmt.Sprintf("conteneur %d créé depuis %d", o.NewID, src),
					Rollback: fmt.Sprintf("pvecli lxc rm %d", o.NewID),
					Verify:   fmt.Sprintf("relecture de la configuration de %d", o.NewID),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeLXC, src)
					if err != nil {
						return service.State{}, fmt.Errorf("source %d illisible : %w", src, err)
					}
					srcIsTemplate = cfg.String("template") == "1"
					if err := owner.check(src, cfg.String("tags"), opCloneSource); err != nil {
						return service.State{}, err
					}
					if _, err := client.GuestStatus(ctx, node, pve.TypeLXC, o.NewID); err == nil {
						return service.State{}, fmt.Errorf("le vmid %d est déjà pris", o.NewID)
					}
					return service.State{Exists: true, Status: "destination libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.CloneGuest(ctx, node, pve.TypeLXC, src, o)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeLXC, o.NewID)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "cloné", Raw: cfg}, nil
				},
			})
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"source", args[0]}, {"nouveau vmid", target},
				{"type", ctCloneKind(srcIsTemplate, o.Full)}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.IntVar(&o.NewID, "newid", 0, "vmid du nouveau conteneur (obligatoire)")
	f.StringVar(&o.Name, "hostname", "", "nom d'hôte du nouveau conteneur")
	f.StringVar(&o.Description, "description", "", "description")
	f.StringVar(&o.Pool, "pool", "", "pool de rattachement")
	f.StringVar(&o.Storage, "storage", "", "stockage cible (clone complet uniquement)")
	f.StringVar(&o.Target, "target", "", "nœud cible")
	f.BoolVar(&o.Full, "full", false, "copie complète plutôt que liée")
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}
