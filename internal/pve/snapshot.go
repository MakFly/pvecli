package pve

import (
	"context"
	"net/url"
	"strconv"
)

// Snapshot is one entry of GET /nodes/{node}/qemu/{vmid}/snapshot.
//
// A snapshot is not a backup, and confusing the two is a classic disaster
// recovery mistake. A snapshot is a local rollback point that lives on the same
// storage as the disk it protects: if that storage dies, the snapshot dies with
// it. A backup (M5) is an independent copy on another storage.
type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parent chains snapshots together: PVE keeps a tree, not a list.
	Parent   string `json:"parent,omitempty"`
	SnapTime int64  `json:"snaptime,omitempty"`
	// VMState is 1 when the guest's memory was saved too, which makes the
	// snapshot restorable to a running state — and much larger.
	VMState int `json:"vmstate,omitempty"`
}

// IsCurrent reports whether this entry is the synthetic "you are here" node PVE
// appends to every snapshot listing. It is not a snapshot and cannot be rolled
// back to.
func (s Snapshot) IsCurrent() bool { return s.Name == "current" }

// Snapshots lists a guest's snapshots.
//
// GET /nodes/{node}/qemu/{vmid}/snapshot
func (c *Client) Snapshots(ctx context.Context, node string, kind GuestType, vmid int) ([]Snapshot, error) {
	e := epQemuSnapshots
	if kind == TypeLXC {
		e = epLXCSnapshots
	}
	var out []Snapshot
	if err := c.get(ctx, e, []string{node, strconv.Itoa(vmid)}, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateSnapshot takes a snapshot and returns the UPID of the task.
//
// POST /nodes/{node}/qemu/{vmid}/snapshot
func (c *Client) CreateSnapshot(ctx context.Context, node string, kind GuestType, vmid int, name, description string, vmstate bool) (string, error) {
	body := url.Values{"snapname": {name}}
	if description != "" {
		body.Set("description", description)
	}
	if vmstate {
		body.Set("vmstate", "1")
	}

	e := epQemuSnapCreate
	if kind == TypeLXC {
		e = epLXCSnapCreate
	}
	var upid string
	if err := c.post(ctx, e, []string{node, strconv.Itoa(vmid)}, body, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// RollbackSnapshot restores a guest to a snapshot.
//
// POST /nodes/{node}/qemu/{vmid}/snapshot/{name}/rollback
func (c *Client) RollbackSnapshot(ctx context.Context, node string, kind GuestType, vmid int, name string) (string, error) {
	e := epQemuSnapRollback
	if kind == TypeLXC {
		e = epLXCSnapRollback
	}
	var upid string
	if err := c.post(ctx, e, []string{node, strconv.Itoa(vmid), name}, url.Values{}, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// DeleteSnapshot removes a snapshot.
//
// DELETE /nodes/{node}/qemu/{vmid}/snapshot/{name}
func (c *Client) DeleteSnapshot(ctx context.Context, node string, kind GuestType, vmid int, name string) (string, error) {
	e := epQemuSnapDelete
	if kind == TypeLXC {
		e = epLXCSnapDelete
	}
	var upid string
	if err := c.del(ctx, e, []string{node, strconv.Itoa(vmid), name}, nil, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// SnapshotPath renders a snapshot path, for --dry-run.
func SnapshotPath(kind GuestType, node string, vmid int, name, action string) string {
	base := epQemuSnapshots
	if kind == TypeLXC {
		base = epLXCSnapshots
	}
	path := base.Path(node, strconv.Itoa(vmid))
	if name != "" {
		path += "/" + name
	}
	if action != "" {
		path += "/" + action
	}
	return path
}
