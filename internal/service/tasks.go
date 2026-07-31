package service

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/MakFly/pvectl/internal/pve"
)

// TaskAPI is the slice of the client the waiter needs.
type TaskAPI interface {
	TaskStatus(ctx context.Context, node, upid string) (*pve.Task, error)
	TaskLog(ctx context.Context, node, upid string, tail int) ([]pve.LogLine, error)
}

// TaskWaiter follows a task to its terminal state.
//
// This is where the central rule of the playbook lives: treat acceptance as the
// beginning of the operation, not as success. An HTTP 200 on a mutation means
// the node queued a job; whether the job worked is in `exitstatus`, minutes
// later, and nowhere else.
type TaskWaiter struct {
	API TaskAPI
	// Progress receives the ticks. stderr, and silent when it is not a
	// terminal — a progress spinner in a CI log is noise.
	Progress io.Writer
	Quiet    bool
}

// Wait polls until the task reaches a terminal state.
//
// The node polled is the one named inside the UPID, never the default node:
// that is the whole reason the UPID carries it.
func (w *TaskWaiter) Wait(ctx context.Context, upid *pve.UPID) (*pve.Task, error) {
	const (
		firstDelay = time.Second
		maxDelay   = 5 * time.Second
	)
	delay := firstDelay

	for {
		task, err := w.API.TaskStatus(ctx, upid.Node, upid.String())
		if err != nil {
			return nil, err
		}
		if task.Status == "stopped" {
			w.tick("\n")
			return task, nil
		}

		w.tick(".")

		select {
		case <-ctx.Done():
			// A cancelled wait does not cancel the task: the node keeps
			// working. Saying so, with the command to resume following, is the
			// difference between an interrupted wait and a lost operation.
			return nil, fmt.Errorf(`attente interrompue — la tâche continue sur le nœud.

Elle n'a PAS été annulée. Pour reprendre le suivi :
  pvectl task wait %s`, upid)

		case <-time.After(delay):
			if delay < maxDelay {
				delay += time.Second
			}
		}
	}
}

// Tail returns the last lines of a task log, which is what a failure is
// actually explained by.
func (w *TaskWaiter) Tail(ctx context.Context, upid *pve.UPID, lines int) ([]string, error) {
	entries, err := w.API.TaskLog(ctx, upid.Node, upid.String(), lines)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Text)
	}
	return out, nil
}

func (w *TaskWaiter) tick(s string) {
	if w.Quiet || w.Progress == nil {
		return
	}
	_, _ = fmt.Fprint(w.Progress, s)
}
