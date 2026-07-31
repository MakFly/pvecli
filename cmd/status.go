package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/MakFly/pvectl/internal/output"
	"github.com/MakFly/pvectl/internal/pve"
	"github.com/MakFly/pvectl/internal/service"
	"github.com/spf13/cobra"
)

// actionHelp documents each transition, endpoint included. The CLI teaches the
// API by being used — which is only true if the help says what it calls.
var actionHelp = map[pve.GuestAction]struct{ short, long string }{
	pve.ActionStart: {"Démarre le guest", `Démarre le guest.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/start`},

	pve.ActionStop: {"Coupe l'alimentation (brutal)", `Coupe l'alimentation du guest, immédiatement.

Ce n'est PAS « shutdown ». Le système invité n'est pas prévenu : ses écritures
en cours sont perdues, et son système de fichiers l'apprendra au redémarrage.
Utilise « stop » quand le guest ne répond plus, pas comme arrêt de routine.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/stop`},

	pve.ActionShutdown: {"Demande un arrêt propre (ACPI)", `Demande au système invité de s'arrêter, via ACPI.

Nécessite un OS coopératif : sans agent ACPI côté guest, il ne se passe rien et
la tâche expire. C'est l'arrêt de routine ; « stop » est le dernier recours.

  --timeout      délai laissé au guest avant abandon
  --force-stop   coupe l'alimentation si le délai expire

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/shutdown`},

	pve.ActionReboot: {"Redémarre proprement", `Redémarre le guest en passant par un arrêt ACPI.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/reboot`},

	pve.ActionReset: {"Réinitialise (brutal)", `Réinitialise le guest sans prévenir le système invité — l'équivalent du bouton
reset. Mêmes conséquences que « stop » sur les écritures en cours.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/reset`},

	pve.ActionSuspend: {"Suspend le guest", `Suspend l'exécution du guest.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/suspend`},

	pve.ActionResume: {"Reprend un guest suspendu", `Reprend l'exécution d'un guest suspendu.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/status/resume`},
}

func newStatusCmd(kind pve.GuestType, action pve.GuestAction) *cobra.Command {
	var (
		shutdownTimeout time.Duration
		forceStop       bool
	)

	help := actionHelp[action]
	destructive := action == pve.ActionStop || action == pve.ActionReset

	c := &cobra.Command{
		Use:   string(action) + " <vmid>",
		Short: help.short,
		Long:  help.long,
		Args:  usage(cobra.ExactArgs(1)),
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
			if action == pve.ActionShutdown {
				if shutdownTimeout > 0 {
					params.Set("timeout", strconv.Itoa(int(shutdownTimeout.Seconds())))
				}
				if forceStop {
					params.Set("forceStop", "1")
				}
			}

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target:      target,
				Destructive: destructive,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.StatusPath(kind, node, vmid, action),
					Payload:  params,
					Effect:   effectOf(action, vmid),
					Rollback: rollbackOf(kind, action, vmid),
					Verify:   fmt.Sprintf("relecture de status/current du guest %d", vmid),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					// Already there: say so, change nothing, exit 0. Sending a
					// start to a running VM is a write nobody asked for.
					if want := action.TargetStatus(); want != "" && st.Status == want {
						return service.State{}, fmt.Errorf("%w (statut %s)", service.ErrAlreadyInState, st.Status)
					}
					return guestState(st), nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.SetGuestStatus(ctx, node, kind, vmid, action, params)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					return guestState(st), nil
				},
			})

			if errors.Is(err, service.ErrAlreadyInState) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "guest %d : %v — aucune écriture émise.\n", vmid, err)
				return nil
			}
			if err != nil {
				return err
			}

			// What gets printed is the post-read, never an echo of the request.
			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"vmid", target}, {"statut", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	if action == pve.ActionShutdown {
		c.Flags().DurationVar(&shutdownTimeout, "shutdown-timeout", 0, "délai laissé au guest pour s'arrêter")
		c.Flags().BoolVar(&forceStop, "force-stop", false, "coupe l'alimentation si le délai expire")
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func effectOf(a pve.GuestAction, vmid int) string {
	switch a {
	case pve.ActionStop, pve.ActionReset:
		return fmt.Sprintf("guest %d coupé sans prévenir le système invité", vmid)
	case pve.ActionShutdown:
		return fmt.Sprintf("guest %d prié de s'arrêter (ACPI)", vmid)
	default:
		return fmt.Sprintf("guest %d → %s", vmid, a.TargetStatus())
	}
}

// rollbackOf names the command that undoes this one. It carries the guest
// family: a plan that tells a container operator to run `pvectl vm shutdown`
// hands them a command that will not work.
func rollbackOf(kind pve.GuestType, a pve.GuestAction, vmid int) string {
	switch a.TargetStatus() {
	case "running":
		return fmt.Sprintf("pvectl %s shutdown %d", cliGroup(kind), vmid)
	case "stopped":
		return fmt.Sprintf("pvectl %s start %d", cliGroup(kind), vmid)
	default:
		return "—"
	}
}

// statusCommands builds the transition subcommands for one guest family.
func statusCommands(kind pve.GuestType) []*cobra.Command {
	actions := []pve.GuestAction{
		pve.ActionStart, pve.ActionStop, pve.ActionShutdown,
		pve.ActionReboot, pve.ActionReset,
	}
	if kind == pve.TypeQEMU {
		actions = append(actions, pve.ActionSuspend, pve.ActionResume)
	}

	out := make([]*cobra.Command, 0, len(actions))
	for _, a := range actions {
		out = append(out, newStatusCmd(kind, a))
	}
	return out
}

func newTaskWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait <upid>",
		Short: "Attend la fin d'une tâche et rapporte son exitstatus",
		Long: `Suit une tâche jusqu'à son état terminal, puis lit son « exitstatus ».

Le nœud interrogé est celui inscrit DANS l'UPID, jamais le nœud par défaut :
c'est précisément pour ça que l'UPID le transporte.

Un HTTP 200 sur une mutation signifie « demande acceptée », rien de plus. Cette
commande est la moitié manquante.`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			upid, err := pve.ParseUPID(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: err.Error()}
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			waiter := &service.TaskWaiter{
				API:      client,
				Progress: cmd.ErrOrStderr(),
				Quiet:    !output.IsTerminal(cmd.ErrOrStderr()),
			}

			task, err := waiter.Wait(cmd.Context(), upid)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s : %s\n", upid.Type, upid.ID, task.ExitStatus)
			if !task.Succeeded() {
				lines, _ := waiter.Tail(cmd.Context(), upid, 20)
				for _, l := range lines {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), l)
				}
				return &service.TaskError{
					UPID: upid.String(),
					Msg:  fmt.Sprintf("la tâche %s a échoué : %s", upid.Type, task.ExitStatus),
				}
			}
			return nil
		},
	}
}
