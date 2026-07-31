// Package service holds the use cases sitting between cmd and pve. It owns the
// mutation contract of PRD §5.3, and it owns it in one place so that no command
// can forget a step.
//
// Everything here takes function values rather than a concrete client, so the
// sequence can be tested with mocks and no Proxmox node powered on.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/MakFly/pvectl/internal/pve"
)

// State is the minimum the pipeline needs to know about a target, before and
// after the write.
type State struct {
	Exists bool
	Lock   string
	Status string

	// Summary is what gets shown to the operator.
	Summary string
	// Raw is the typed value, for -o json.
	Raw any
}

// Plan is what is about to be sent — the resolved payload, not a paraphrase of
// it. An honest --dry-run is the best teaching tool in the CLI: it shows the
// API call you are about to make.
type Plan struct {
	Node    string
	Method  string
	Path    string
	Payload url.Values

	Effect   string
	Rollback string
	Verify   string
}

// String renders the plan for stderr.
func (p Plan) String() string {
	var b strings.Builder
	// Not every mutation targets a node: /access/acl and the token endpoints
	// are cluster-wide. Printing an empty "nœud" line would invent a scope.
	if p.Node != "" {
		fmt.Fprintf(&b, "  nœud     %s\n", p.Node)
	}
	fmt.Fprintf(&b, "  requête  %s %s\n", p.Method, p.Path)

	if len(p.Payload) > 0 {
		keys := make([]string, 0, len(p.Payload))
		for k := range p.Payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "  payload\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "    %-16s %s\n", k, redactValue(k, p.Payload.Get(k)))
		}
	}

	if p.Effect != "" {
		fmt.Fprintf(&b, "  effet    %s\n", p.Effect)
	}
	if p.Rollback != "" {
		fmt.Fprintf(&b, "  retour   %s\n", p.Rollback)
	}
	if p.Verify != "" {
		fmt.Fprintf(&b, "  preuve   %s\n", p.Verify)
	}
	return b.String()
}

// redactValue keeps a plan printable without turning it into a secret leak.
func redactValue(key, value string) string {
	switch strings.ToLower(key) {
	case "password", "sshkeys", "ssh-public-keys", "cipassword", "token_secret", "secret":
		return "<redacted>"
	default:
		return value
	}
}

// Mutation is one write, described end to end.
type Mutation struct {
	Plan Plan

	// Target names what is being changed, for the confirmation prompt.
	Target string
	// Destructive raises the bar: the operator has to retype Target rather
	// than answer yes.
	Destructive bool

	// PreRead must report whether the target exists and whether it is locked.
	PreRead func(context.Context) (State, error)
	// Write performs the mutation. It returns a UPID when the node answered
	// with one, or the empty string for a synchronous mutation.
	Write func(context.Context) (string, error)
	// PostRead re-reads the target: the independent proof that it worked.
	PostRead func(context.Context) (State, error)
}

// Gate decides whether a write may proceed.
type Gate interface {
	// Allow returns nil to proceed, or an error carrying exit code 5.
	Allow(target string, destructive bool) error
}

// Waiter follows a task to its terminal state.
type Waiter interface {
	Wait(ctx context.Context, upid *pve.UPID) (*pve.Task, error)
	Tail(ctx context.Context, upid *pve.UPID, lines int) ([]string, error)
}

// Runner executes mutations according to PRD §5.3.
type Runner struct {
	// Progress receives the plan, the confirmation and the task progress:
	// stderr, never stdout, so that `--dry-run -o json` stays pipeable.
	Progress io.Writer
	DryRun   bool
	Gate     Gate
	Waiter   Waiter
}

// ErrAlreadyInState is returned when the target is already where the command
// would take it. Not an error condition: exit code 0, and no write is sent.
var ErrAlreadyInState = errors.New("déjà dans l'état demandé")

// Run executes the seven steps, in order, every time.
//
//  1. pre-read   the target exists? is it locked?
//  2. plan       render what is about to be sent
//  3. gate       --dry-run stops here; otherwise confirm
//  4. write
//  5. poll       a 200 is an acceptance, not a success
//  6. log        on failure, the last lines of the task log
//  7. post-read  the independent proof
//
// Steps 5 and 6 are skipped for a synchronous mutation. Step 7 never is: the
// result shown to the operator is the post-read, not an echo of the request.
func (r *Runner) Run(ctx context.Context, m Mutation) (*State, error) {
	before, err := m.PreRead(ctx)
	if err != nil {
		return nil, err
	}
	if !before.Exists {
		return nil, fmt.Errorf("%s n'existe pas", m.Target)
	}
	if before.Lock != "" {
		return nil, fmt.Errorf("%s est verrouillé (lock=%s) — une tâche est en cours :\n  pvectl task ls --running",
			m.Target, before.Lock)
	}

	_, _ = fmt.Fprintf(r.Progress, "\n%s\n", m.Plan.String())

	if r.DryRun {
		_, _ = fmt.Fprintln(r.Progress, "  --dry-run : aucune requête d'écriture émise.")
		return &before, nil
	}

	if err := r.Gate.Allow(m.Target, m.Destructive); err != nil {
		return nil, err
	}

	raw, err := m.Write(ctx)
	if err != nil {
		return nil, err
	}

	if pve.IsUPID(raw) {
		upid, err := pve.ParseUPID(raw)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(r.Progress, "tâche %s\n", upid)

		task, err := r.Waiter.Wait(ctx, upid)
		if err != nil {
			return nil, err
		}
		if !task.Succeeded() {
			lines, logErr := r.Waiter.Tail(ctx, upid, 20)
			msg := fmt.Sprintf("la tâche %s a échoué : %s", upid.Type, task.ExitStatus)
			if logErr == nil && len(lines) > 0 {
				msg += "\n\n" + strings.Join(lines, "\n")
			}
			return nil, &TaskError{UPID: upid.String(), Msg: msg}
		}
		// A task that did its work and had something to say ends on
		// "WARNINGS: n". The mutation stands — but silence would hide the
		// warning, and treating it as a failure would deny a change that
		// happened. Both are lies; this is neither.
		if task.HasWarnings() {
			_, _ = fmt.Fprintf(r.Progress, "la tâche %s a réussi avec des avertissements : %s\n",
				upid.Type, task.ExitStatus)
			if lines, err := r.Waiter.Tail(ctx, upid, 20); err == nil {
				for _, l := range lines {
					_, _ = fmt.Fprintln(r.Progress, "  "+l)
				}
			}
		}
	}

	after, err := m.PostRead(ctx)
	if err != nil {
		return nil, err
	}
	return &after, nil
}

// TaskError is a PVE task that ran and failed — exit code 4, distinct from a
// request that was refused.
type TaskError struct {
	UPID string
	Msg  string
}

func (e *TaskError) Error() string { return e.Msg }
func (e *TaskError) ExitCode() int { return pve.ExitTask }
