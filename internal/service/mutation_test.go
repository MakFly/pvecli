package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/pve"
)

const testUPID = "UPID:pve:0011A2B3:000043CD:6A6CAD90:qmstart:210:automation@pve:"

// recorder captures the order of the pipeline's steps.
type recorder struct {
	calls []string
	upid  string
	fail  string
}

func (r *recorder) mutation() Mutation {
	return Mutation{
		Target: "210",
		Plan:   Plan{Node: "pve", Method: "POST", Path: "/nodes/pve/qemu/210/status/start"},
		PreRead: func(context.Context) (State, error) {
			r.calls = append(r.calls, "pre-read")
			return State{Exists: true, Status: "stopped"}, nil
		},
		Write: func(context.Context) (string, error) {
			r.calls = append(r.calls, "write")
			return r.upid, nil
		},
		PostRead: func(context.Context) (State, error) {
			r.calls = append(r.calls, "post-read")
			return State{Exists: true, Status: "running"}, nil
		},
	}
}

func (r *recorder) Wait(context.Context, *pve.UPID) (*pve.Task, error) {
	r.calls = append(r.calls, "poll")
	status := "OK"
	if r.fail != "" {
		status = r.fail
	}
	return &pve.Task{Status: "stopped", ExitStatus: status}, nil
}

func (r *recorder) Tail(context.Context, *pve.UPID, int) ([]string, error) {
	r.calls = append(r.calls, "log")
	return []string{"TASK ERROR: échec simulé"}, nil
}

type openGate struct{ err error }

func (g openGate) Allow(string, bool) error { return g.err }

func newRunner(r *recorder, dryRun bool, gate Gate) *Runner {
	return &Runner{Progress: io.Discard, DryRun: dryRun, Gate: gate, Waiter: r}
}

// The contract of PRD §5.3, asserted as an ordered sequence.
func TestMutationPipelineOrder(t *testing.T) {
	rec := &recorder{upid: testUPID}

	if _, err := newRunner(rec, false, openGate{}).Run(context.Background(), rec.mutation()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"pre-read", "write", "poll", "post-read"}
	if strings.Join(rec.calls, ",") != strings.Join(want, ",") {
		t.Errorf("séquence = %v, want %v", rec.calls, want)
	}
}

// The post-read is what makes a write proven rather than assumed. A pipeline
// that skips it is the regression this test exists for.
func TestPostReadIsNeverSkipped(t *testing.T) {
	rec := &recorder{upid: ""} // mutation synchrone : pas d'UPID

	if _, err := newRunner(rec, false, openGate{}).Run(context.Background(), rec.mutation()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"pre-read", "write", "post-read"}
	if strings.Join(rec.calls, ",") != strings.Join(want, ",") {
		t.Errorf("séquence = %v, want %v — le polling se saute, pas le post-read", rec.calls, want)
	}
}

func TestDryRunEmitsNoWrite(t *testing.T) {
	rec := &recorder{upid: testUPID}

	if _, err := newRunner(rec, true, openGate{}).Run(context.Background(), rec.mutation()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Join(rec.calls, ",") != "pre-read" {
		t.Errorf("séquence = %v — --dry-run doit s'arrêter après le pre-read", rec.calls)
	}
}

// A refused confirmation stops before the write, not after it.
func TestRefusedGateStopsBeforeWrite(t *testing.T) {
	rec := &recorder{upid: testUPID}
	refused := errors.New("confirmation refusée")

	_, err := newRunner(rec, false, openGate{err: refused}).Run(context.Background(), rec.mutation())
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want %v", err, refused)
	}
	if strings.Join(rec.calls, ",") != "pre-read" {
		t.Errorf("séquence = %v — rien ne doit être écrit après un refus", rec.calls)
	}
}

// A lock found at pre-read aborts, and points at what holds it.
func TestLockAbortsBeforeWrite(t *testing.T) {
	rec := &recorder{upid: testUPID}
	m := rec.mutation()
	m.PreRead = func(context.Context) (State, error) {
		rec.calls = append(rec.calls, "pre-read")
		return State{Exists: true, Lock: "backup"}, nil
	}

	_, err := newRunner(rec, false, openGate{}).Run(context.Background(), m)
	if err == nil {
		t.Fatal("un verrou doit interrompre avant l'écriture")
	}
	if !strings.Contains(err.Error(), "task ls --running") {
		t.Errorf("l'erreur doit dire où regarder: %v", err)
	}
	if strings.Join(rec.calls, ",") != "pre-read" {
		t.Errorf("séquence = %v", rec.calls)
	}
}

// A task that ran and failed is exit code 4, with the tail of its log — not a
// generic error, and not a success.
func TestFailedTaskFetchesTheLog(t *testing.T) {
	rec := &recorder{upid: testUPID, fail: "disque plein"}

	_, err := newRunner(rec, false, openGate{}).Run(context.Background(), rec.mutation())
	if err == nil {
		t.Fatal("un exitstatus différent de OK doit échouer")
	}

	var taskErr *TaskError
	if !errors.As(err, &taskErr) {
		t.Fatalf("err = %T, want *TaskError", err)
	}
	if taskErr.ExitCode() != pve.ExitTask {
		t.Errorf("ExitCode() = %d, want %d", taskErr.ExitCode(), pve.ExitTask)
	}
	if !strings.Contains(err.Error(), "échec simulé") {
		t.Errorf("les dernières lignes du log doivent remonter: %v", err)
	}
	if strings.Join(rec.calls, ",") != "pre-read,write,poll,log" {
		t.Errorf("séquence = %v — pas de post-read après un échec", rec.calls)
	}
}

// The plan shows the resolved payload, and hides what must not be shown.
func TestPlanRedactsSecrets(t *testing.T) {
	p := Plan{
		Node: "pve", Method: "POST", Path: "/nodes/pve/qemu",
		Payload: map[string][]string{
			"name":     {"lab-app-01"},
			"cipasswd": {"visible"},
			"sshkeys":  {"ssh-ed25519 AAAA..."},
			"password": {"hunter2"},
		},
	}
	out := p.String()

	if strings.Contains(out, "hunter2") || strings.Contains(out, "ssh-ed25519") {
		t.Errorf("le plan expose un secret:\n%s", out)
	}
	if !strings.Contains(out, "lab-app-01") {
		t.Errorf("le plan doit montrer le payload résolu:\n%s", out)
	}
}
