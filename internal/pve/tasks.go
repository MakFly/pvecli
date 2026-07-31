package pve

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Task is one entry of GET /nodes/{node}/tasks.
//
// Every click in the web interface leaves one of these. Reading them is the
// cheapest way to learn which endpoint the UI calls for a given action.
type Task struct {
	UPID   string `json:"upid"`
	Node   string `json:"node"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	User   string `json:"user"`
	Status string `json:"status,omitempty"`

	// ExitStatus is "OK" on success, "WARNINGS: n" on a success with something
	// to report, and carries the reason on failure. It is the field that
	// decides whether a mutation actually worked: an HTTP 200 only means the
	// node accepted the job. Read it through Succeeded(), never by comparing
	// it to "OK".
	ExitStatus string `json:"exitstatus,omitempty"`

	StartTime int64 `json:"starttime"`
	EndTime   int64 `json:"endtime,omitempty"`
	PID       int   `json:"pid,omitempty"`
}

// Running reports whether the task is still in flight.
func (t Task) Running() bool { return t.EndTime == 0 && t.Status != "stopped" }

// Succeeded reports whether the task reached its goal.
//
// "OK" is not the only successful exitstatus. A task that did its work and
// noticed something on the way reports "WARNINGS: n" — a container creation
// warning about systemd nesting, for instance. Treating that as a failure made
// pvecli announce a failed creation over a container that existed, and skip the
// post-read that would have shown it. Observed on the lab at PVX-030.
func (t Task) Succeeded() bool {
	return t.ExitStatus == "OK" || strings.HasPrefix(t.ExitStatus, "WARNINGS:")
}

// HasWarnings reports whether the task succeeded with something to say.
func (t Task) HasWarnings() bool { return strings.HasPrefix(t.ExitStatus, "WARNINGS:") }

// Tasks lists recent tasks on a node.
//
// GET /nodes/{node}/tasks
func (c *Client) Tasks(ctx context.Context, node string, onlyRunning bool, limit int) ([]Task, error) {
	query := url.Values{}
	if onlyRunning {
		// `source=active`, not `running=1`. Verified with
		// `pvesh usage /nodes/pve/tasks -v`: the endpoint lists *finished*
		// tasks by default (source=archive), and there is no `running`
		// parameter at all — inventing one earns a 400.
		query.Set("source", "active")
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	var out []Task
	if err := c.get(ctx, epTasks, []string{node}, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// TaskStatus reads one task's state.
//
// GET /nodes/{node}/tasks/{upid}/status
func (c *Client) TaskStatus(ctx context.Context, node, upid string) (*Task, error) {
	var out Task
	if err := c.get(ctx, epTaskStatus, []string{node, upid}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LogLine is one line of a task log, with its position in the stream.
type LogLine struct {
	N    int    `json:"n"`
	Text string `json:"t"`
}

// TaskLog reads a task's log. tail limits the output to the last N lines,
// which is what matters when a mutation failed.
//
// GET /nodes/{node}/tasks/{upid}/log
func (c *Client) TaskLog(ctx context.Context, node, upid string, tail int) ([]LogLine, error) {
	query := url.Values{}
	if tail > 0 {
		query.Set("limit", strconv.Itoa(tail))
		// Without this, `limit` counts from the start of the log, and the
		// tail of a long log is exactly the part nobody gets to see.
		query.Set("start", "0")
		query.Set("download", "0")
	}

	var out []LogLine
	if err := c.get(ctx, epTaskLog, []string{node, upid}, query, &out); err != nil {
		return nil, err
	}
	if tail > 0 && len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out, nil
}

// Resource is one entry of GET /cluster/resources: a single call that replaces
// looping over every node.
type Resource struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Node    string `json:"node,omitempty"`
	Status  string `json:"status,omitempty"`
	Name    string `json:"name,omitempty"`
	VMID    int    `json:"vmid,omitempty"`
	Storage string `json:"storage,omitempty"`
	// Pool is the resource's own id on a `pool` entry, and the pool a guest
	// belongs to on a `qemu`/`lxc` entry. Same field, two readings.
	Pool    string  `json:"pool,omitempty"`
	CPU     float64 `json:"cpu,omitempty"`
	MaxCPU  int     `json:"maxcpu,omitempty"`
	Mem     int64   `json:"mem,omitempty"`
	MaxMem  int64   `json:"maxmem,omitempty"`
	Disk    int64   `json:"disk,omitempty"`
	MaxDisk int64   `json:"maxdisk,omitempty"`
	Uptime  int64   `json:"uptime,omitempty"`
	Tags    string  `json:"tags,omitempty"`

	Template int `json:"template,omitempty"`
}

// Resources reads the whole cluster inventory in one call, optionally filtered
// by type (vm, storage, node, sdn).
//
// GET /cluster/resources
func (c *Client) Resources(ctx context.Context, kind string) ([]Resource, error) {
	var query url.Values
	if kind != "" {
		query = url.Values{"type": {kind}}
	}
	var out []Resource
	if err := c.get(ctx, epClusterRes, nil, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}
