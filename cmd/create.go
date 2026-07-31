package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/service"
	"github.com/spf13/cobra"
)

type createOpts struct {
	name       string
	cores      int
	memory     int
	storage    string
	diskSize   string
	importFrom string
	bridge     string
	osType     string
	tags       string

	cloudInit bool
	ciUser    string
	sshKeys   string
	ipConfig  string
	agent     bool
	start     bool
}

func newVMCreateCmd() *cobra.Command {
	o := createOpts{}

	c := &cobra.Command{
		Use:   "create <vmid>",
		Short: "Crée une machine virtuelle (POST /nodes/{node}/qemu)",
		Long: `Crée une VM QEMU.

Avec --import-from, le disque est alloué et rempli depuis une image disque déjà
présente sur le nœud, en une seule requête :

    scsi0: local-lvm:0,import-from=/var/lib/vz/import/image.qcow2

Le « 0 » veut dire « taille prise dans l'image ». C'est la forme moderne, qui
remplace l'ancien « qm importdisk » en deux temps.

--cloud-init ajoute le lecteur cloud-init, le disque de démarrage et la console
série que les images cloud attendent. Sans lui, une image cloud démarre mais
reste inaccessible : aucun utilisateur, aucune clé, aucun réseau configuré.

Endpoint : POST /api2/json/nodes/{node}/qemu`,
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

			params, err := o.payload(vmid)
			if err != nil {
				return err
			}

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.CreatePath(pve.TypeQEMU, node),
					Payload:  params,
					Effect:   fmt.Sprintf("création de la VM %d sur %s", vmid, node),
					Rollback: fmt.Sprintf("pvecli vm rm %d", vmid),
					Verify:   fmt.Sprintf("relecture de la configuration de %d", vmid),
				},
				// Creation inverts the pre-read: what must be true is that the
				// vmid is FREE. A 404 here is the success condition.
				PreRead: func(ctx context.Context) (service.State, error) {
					_, err := client.GuestStatus(ctx, node, pve.TypeQEMU, vmid)
					if err == nil {
						return service.State{}, fmt.Errorf("le vmid %d est déjà pris — choisis-en un autre ou supprime l'existant", vmid)
					}
					return service.State{Exists: true, Status: "libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.CreateGuest(ctx, node, pve.TypeQEMU, vmid, params)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeQEMU, vmid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{
						Exists:  true,
						Status:  "created",
						Summary: fmt.Sprintf("VM %d créée", vmid),
						Raw:     cfg,
					}, nil
				},
			})
			if err != nil {
				return err
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun && o.start {
				if _, err := client.SetGuestStatus(cmd.Context(), node, pve.TypeQEMU, vmid, pve.ActionStart, nil); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "démarrage demandé — suis-le avec « pvecli task ls --running »\n")
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"vmid", target}, {"nœud", node}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.StringVar(&o.name, "name", "", "nom de la VM")
	f.IntVar(&o.cores, "cores", 2, "nombre de cœurs")
	f.IntVar(&o.memory, "memory", 2048, "mémoire en Mio")
	f.StringVar(&o.storage, "storage", "local-lvm", "stockage du disque système")
	f.StringVar(&o.diskSize, "disk-size", "", "taille du disque (ex. 20G) — ignoré avec --import-from")
	f.StringVar(&o.importFrom, "import-from", "", "chemin, sur le nœud, d'une image disque à importer")
	f.StringVar(&o.bridge, "bridge", "vmbr0", "pont réseau")
	f.StringVar(&o.osType, "ostype", "l26", "type d'OS (l26 = Linux 2.6+)")
	f.StringVar(&o.tags, "tags", "", "tags, séparés par des virgules")
	f.BoolVar(&o.cloudInit, "cloud-init", false, "ajoute le lecteur cloud-init, le boot et la console série")
	f.StringVar(&o.ciUser, "ci-user", "", "utilisateur cloud-init")
	f.StringVar(&o.sshKeys, "ssh-keys", "", "fichier de clés publiques SSH à injecter")
	f.StringVar(&o.ipConfig, "ip", "dhcp", "configuration IP cloud-init : dhcp, ou ip=…/…,gw=…")
	f.BoolVar(&o.agent, "agent", true, "déclare l'agent QEMU")
	f.BoolVar(&o.start, "start", false, "démarre la VM après création")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// payload resolves every flag into the exact parameters that will be sent.
// --dry-run shows this, unchanged: a plan that paraphrases teaches nothing.
func (o createOpts) payload(vmid int) (url.Values, error) {
	p := url.Values{}
	if o.name != "" {
		p.Set("name", o.name)
	}
	p.Set("cores", strconv.Itoa(o.cores))
	p.Set("memory", strconv.Itoa(o.memory))
	p.Set("ostype", o.osType)
	// virtio-scsi-single is what a modern Linux guest expects, and what the
	// web interface picks by default.
	p.Set("scsihw", "virtio-scsi-single")
	p.Set("net0", "virtio,bridge="+o.bridge)

	switch {
	case o.importFrom != "":
		// "storage:0" means "allocate here, take the size from the image".
		p.Set("scsi0", fmt.Sprintf("%s:0,import-from=%s", o.storage, o.importFrom))
	case o.diskSize != "":
		size := strings.TrimSuffix(strings.TrimSuffix(o.diskSize, "G"), "g")
		p.Set("scsi0", fmt.Sprintf("%s:%s", o.storage, size))
	default:
		return nil, &exitError{
			code: pve.ExitUsage,
			msg:  "il faut --import-from (image existante) ou --disk-size (disque vide)",
		}
	}

	if o.agent {
		p.Set("agent", "1")
	}
	if o.tags != "" {
		p.Set("tags", strings.ReplaceAll(o.tags, ",", ";"))
	}

	if o.cloudInit {
		p.Set("ide2", o.storage+":cloudinit")
		p.Set("boot", "order=scsi0")
		// Cloud images log to the serial console and ship no VGA driver;
		// without these two, a boot failure is invisible.
		p.Set("serial0", "socket")
		p.Set("vga", "serial0")

		if o.ciUser != "" {
			p.Set("ciuser", o.ciUser)
		}
		if o.ipConfig != "" {
			p.Set("ipconfig0", "ip="+o.ipConfig)
		}
		if o.sshKeys != "" {
			raw, err := os.ReadFile(o.sshKeys)
			if err != nil {
				return nil, fmt.Errorf("lecture des clés SSH : %w", err)
			}
			p.Set("sshkeys", encodeSSHKeys(string(raw)))
		}
	}

	return p, nil
}

// readSSHKeys loads a public-key file and encodes it the way PVE wants it.
func readSSHKeys(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("lecture des clés SSH : %w", err)
	}
	return encodeSSHKeys(string(raw)), nil
}

// encodeSSHKeys percent-encodes the public keys the way PVE expects them.
//
// PVE stores `sshkeys` URL-encoded and uri_unescape()s it when it builds the
// cloud-init drive (PVE/QemuServer/Cloudinit.pm). The value therefore has to be
// escaped here, on top of the form encoding.
//
// url.QueryEscape alone is not enough: it encodes a space as '+', which is a
// convention of HTML form bodies, not of RFC 3986. PVE decodes with
// URI::Escape, where '+' means a literal plus — so it rejects the value with
// "invalid urlencoded string". A space has to be %20.
func encodeSSHKeys(raw string) string {
	return strings.ReplaceAll(url.QueryEscape(strings.TrimSpace(raw)), "+", "%20")
}

func newGuestRemoveCmd(kind pve.GuestType) *cobra.Command {
	var o pve.DeleteOptions

	noun, forceHint := "la VM", "même si elle tourne"
	if kind == pve.TypeLXC {
		noun, forceHint = "le conteneur", "même s'il tourne"
	}

	c := &cobra.Command{
		Use:     "rm <vmid>",
		Aliases: []string{"destroy"},
		Short:   fmt.Sprintf("Détruit %s (DELETE /nodes/{node}/%s/{vmid})", noun, kind),
		Long: fmt.Sprintf(`Détruit %s et ses disques.

Opération destructive : la confirmation exige de retaper le vmid, pas « y ».

  --purge                        retire aussi le guest des jobs de sauvegarde,
                                 des jobs de réplication et des ressources HA.
                                 Sans lui, ces entrées survivent et pointent
                                 vers un guest qui n'existe plus.
  --destroy-unreferenced-disks   efface en plus les volumes portant ce vmid que
                                 la configuration ne référence plus — les restes
                                 d'un disque détaché.
  --force                        détruit %s.
%s

Endpoint : DELETE /api2/json/nodes/{node}/%s/{vmid}`, noun, forceHint, ownershipHelp, kind),
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
			owner, err := newOwnership(cmd)
			if err != nil {
				return err
			}

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)
			// Read by the write step: only a guest that was actually running
			// needs to be stopped first.
			running := false

			_, err = runner.Run(cmd.Context(), service.Mutation{
				Target:      target,
				Destructive: true,
				Plan: service.Plan{
					Node:     node,
					Method:   "DELETE",
					Path:     pve.DeletePath(kind, node, vmid),
					Payload:  o.Values(kind),
					Effect:   fmt.Sprintf("destruction du guest %d et de ses disques", vmid),
					Rollback: "aucun — restauration depuis une sauvegarde uniquement",
					Verify:   fmt.Sprintf("status/current de %d doit répondre 500/404", vmid),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}

					// The ownership guard runs first, and before any write: a
					// guest owned by Terraform must not be destroyed here even
					// if every other condition is met.
					if err := owner.check(vmid, st.Tags, opDestroy); err != nil {
						return service.State{}, err
					}

					running = st.Status == "running"
					if running && !o.Force {
						return service.State{}, fmt.Errorf(
							"le guest %d tourne — arrête-le d'abord :\n  pvecli %s shutdown %d\nou passe --force, qui l'arrête avant de détruire", vmid, cliGroup(kind), vmid)
					}
					return guestState(st), nil
				},
				Write: func(ctx context.Context) (string, error) {
					if running && o.Force && kind != pve.TypeLXC {
						if err := stopBeforeDelete(ctx, cmd, client, node, kind, vmid); err != nil {
							return "", err
						}
					}
					return client.DeleteGuest(ctx, node, kind, vmid, o)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					// The proof of a deletion is that the read now fails.
					if _, err := client.GuestStatus(ctx, node, kind, vmid); err == nil {
						return service.State{}, fmt.Errorf("le guest %d répond encore — la destruction n'a pas abouti", vmid)
					}
					return service.State{Exists: false, Status: "détruit"}, nil
				},
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "guest %d détruit.\n", vmid)
			return nil
		},
	}

	f := c.Flags()
	f.BoolVar(&o.Purge, "purge", true, "retire aussi le guest des jobs de sauvegarde, de réplication et des ressources HA")
	f.BoolVar(&o.DestroyUnreferencedDisks, "destroy-unreferenced-disks", false, "efface aussi les volumes de ce vmid que la configuration ne référence plus")
	f.BoolVar(&o.Force, "force", false, "détruit même si le guest tourne")
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// stopBeforeDelete powers a guest off and waits for the task, because DELETE on
// a running QEMU guest fails outright — the LXC endpoint takes a `force`
// parameter and does this itself, the QEMU one does not.
//
// It says so on stderr: --force does two things, and hiding the first one would
// make a command destroy more than the operator read.
func stopBeforeDelete(ctx context.Context, cmd *cobra.Command, client *pve.Client, node string, kind pve.GuestType, vmid int) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "--force : arrêt du guest %d avant destruction.\n", vmid)

	upid, err := client.SetGuestStatus(ctx, node, kind, vmid, pve.ActionStop, nil)
	if err != nil {
		return err
	}
	if !pve.IsUPID(upid) {
		return nil
	}
	parsed, err := pve.ParseUPID(upid)
	if err != nil {
		return err
	}
	waiter := &service.TaskWaiter{API: client, Progress: cmd.ErrOrStderr(), Quiet: true}
	task, err := waiter.Wait(ctx, parsed)
	if err != nil {
		return err
	}
	if !task.Succeeded() {
		return fmt.Errorf("l'arrêt forcé du guest %d a échoué : %s", vmid, task.ExitStatus)
	}
	return nil
}
