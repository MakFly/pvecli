package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MakFly/pvecli/internal/pve"
)

// fakeTaskAPI answers a scripted sequence of task states, and records which
// node each poll was addressed to.
type fakeTaskAPI struct {
	states []pve.Task
	polls  int
	nodes  []string

	log    []pve.LogLine
	logErr error
	err    error
}

func (f *fakeTaskAPI) TaskStatus(_ context.Context, node, _ string) (*pve.Task, error) {
	f.nodes = append(f.nodes, node)
	if f.err != nil {
		return nil, f.err
	}
	i := f.polls
	f.polls++
	if i >= len(f.states) {
		i = len(f.states) - 1
	}
	st := f.states[i]
	return &st, nil
}

func (f *fakeTaskAPI) TaskLog(_ context.Context, _, _ string, _ int) ([]pve.LogLine, error) {
	if f.logErr != nil {
		return nil, f.logErr
	}
	return f.log, nil
}

func mustUPID(t *testing.T, raw string) *pve.UPID {
	t.Helper()
	upid, err := pve.ParseUPID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return upid
}

// A task that is still running is polled again; the terminal state is the one
// that gets returned. An HTTP 200 was only an acceptance.
func TestWaitPollsUntilTheTaskStops(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{
		{Status: "running"},
		{Status: "running"},
		{Status: "stopped", ExitStatus: "OK"},
	}}
	w := &TaskWaiter{API: api, Progress: io.Discard, Quiet: true}

	task, err := w.Wait(context.Background(), mustUPID(t, testUPID))
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !task.Succeeded() {
		t.Errorf("exitstatus = %q", task.ExitStatus)
	}
	if api.polls != 3 {
		t.Errorf("%d interrogations, want 3", api.polls)
	}
}

// The node polled is the one named INSIDE the UPID, never the default node —
// which is the whole reason a UPID carries it.
func TestWaitPollsTheNodeNamedInTheUPID(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{{Status: "stopped", ExitStatus: "OK"}}}
	w := &TaskWaiter{API: api, Quiet: true}

	upid := mustUPID(t, "UPID:pve2:0011A2B3:000043CD:6A6CAD90:qmstart:210:automation@pve:")
	if _, err := w.Wait(context.Background(), upid); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(api.nodes) == 0 || api.nodes[0] != "pve2" {
		t.Errorf("nœud interrogé = %v, want pve2", api.nodes)
	}
}

// A cancelled wait must not read like a cancelled task: the node keeps
// working, and the message has to say so and hand back the way to resume.
func TestWaitSaysTheTaskSurvivesACancelledWait(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{{Status: "running"}}}
	w := &TaskWaiter{API: api, Quiet: true}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.Wait(ctx, mustUPID(t, testUPID))
	if err == nil {
		t.Fatal("une attente annulée doit rendre une erreur")
	}
	if !strings.Contains(err.Error(), "PAS été annulée") {
		t.Errorf("le message doit dire que la tâche continue : %v", err)
	}
	if !strings.Contains(err.Error(), "pvecli task wait") {
		t.Errorf("le message doit rendre le moyen de reprendre le suivi : %v", err)
	}
}

func TestWaitPropagatesAReadFailure(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{{}}, err: errors.New("403")}
	w := &TaskWaiter{API: api, Quiet: true}

	if _, err := w.Wait(context.Background(), mustUPID(t, testUPID)); err == nil {
		t.Error("une lecture qui échoue ne doit pas passer pour une tâche en cours")
	}
}

func TestTailReturnsTheLogText(t *testing.T) {
	api := &fakeTaskAPI{
		states: []pve.Task{{Status: "stopped"}},
		log: []pve.LogLine{
			{N: 1, Text: "creating disk"},
			{N: 2, Text: "TASK ERROR: no space left"},
		},
	}
	w := &TaskWaiter{API: api, Quiet: true}

	lines, err := w.Tail(context.Background(), mustUPID(t, testUPID), 20)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(lines) != 2 || lines[1] != "TASK ERROR: no space left" {
		t.Errorf("lignes = %v", lines)
	}
}

func TestTailPropagatesItsFailure(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{{Status: "stopped"}}, logErr: errors.New("404")}
	w := &TaskWaiter{API: api, Quiet: true}

	if _, err := w.Tail(context.Background(), mustUPID(t, testUPID), 20); err == nil {
		t.Error("un journal illisible doit remonter, pas devenir un journal vide")
	}
}

// A spinner in a CI log is noise, not progress — so Quiet has to actually
// silence it, and its absence has to actually show something.
func TestProgressIsSilentWhenQuiet(t *testing.T) {
	var buf bytes.Buffer
	api := &fakeTaskAPI{states: []pve.Task{{Status: "stopped", ExitStatus: "OK"}}}

	quiet := &TaskWaiter{API: api, Progress: &buf, Quiet: true}
	if _, err := quiet.Wait(context.Background(), mustUPID(t, testUPID)); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("sortie en mode quiet = %q", buf.String())
	}

	buf.Reset()
	loud := &TaskWaiter{API: api, Progress: &buf}
	api.polls = 0
	if _, err := loud.Wait(context.Background(), mustUPID(t, testUPID)); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Error("hors mode quiet, l'attente doit montrer quelque chose")
	}
}

// A nil Progress must not panic: several commands build a waiter without one.
func TestProgressToleratesANilWriter(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{{Status: "stopped", ExitStatus: "OK"}}}
	w := &TaskWaiter{API: api}

	if _, err := w.Wait(context.Background(), mustUPID(t, testUPID)); err != nil {
		t.Fatal(err)
	}
}

// The first delay is a full second, so a test that polls twice would sleep.
// Keeping the scripted sequence terminal on the first read is what makes this
// suite instant — and this test states that assumption out loud.
func TestWaitReturnsImmediatelyOnATerminalTask(t *testing.T) {
	api := &fakeTaskAPI{states: []pve.Task{{Status: "stopped", ExitStatus: "OK"}}}
	w := &TaskWaiter{API: api, Quiet: true}

	started := time.Now()
	if _, err := w.Wait(context.Background(), mustUPID(t, testUPID)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("une tâche déjà terminée a coûté %s d'attente", elapsed)
	}
}
