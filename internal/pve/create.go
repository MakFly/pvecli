package pve

import (
	"context"
	"net/url"
	"strconv"
)

// CreateGuest asks the node to create a guest and returns the UPID of the task
// it queued.
//
// Every parameter is passed through as given: the CLI resolves the payload,
// this function does not invent anything. Widening a requested write is the
// one thing PRD §7.6 forbids outright.
//
// POST /nodes/{node}/qemu  ·  POST /nodes/{node}/lxc
func (c *Client) CreateGuest(ctx context.Context, node string, kind GuestType, vmid int, params url.Values) (string, error) {
	body := url.Values{}
	for k, v := range params {
		body[k] = v
	}
	body.Set("vmid", strconv.Itoa(vmid))

	e := epQemuCreate
	if kind == TypeLXC {
		e = epLXCCreate
	}
	var upid string
	if err := c.post(ctx, e, []string{node}, body, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// DeleteOptions are the three switches of a destruction. They are separate on
// purpose: PVE treats them separately, and folding them into one flag would
// mean a command doing more than it was asked to.
type DeleteOptions struct {
	// Purge removes the guest from backup jobs, replication jobs and HA.
	// Without it those entries survive, pointing at nothing.
	Purge bool
	// DestroyUnreferencedDisks also wipes volumes carrying this VMID that the
	// configuration no longer references — leftovers from a detached disk.
	DestroyUnreferencedDisks bool
	// Force destroys an LXC container even while it runs. QEMU has no such
	// parameter: for a VM the CLI stops it first instead.
	Force bool
}

// Values renders the options as the node expects them.
func (o DeleteOptions) Values(kind GuestType) url.Values {
	v := url.Values{}
	if o.Purge {
		v.Set("purge", "1")
	}
	if o.DestroyUnreferencedDisks {
		v.Set("destroy-unreferenced-disks", "1")
	}
	// Only the LXC endpoint declares `force`; sending it to QEMU would be an
	// unknown parameter, which PVE rejects outright.
	if o.Force && kind == TypeLXC {
		v.Set("force", "1")
	}
	return v
}

// DeleteGuest destroys a guest and returns the UPID of the task.
//
// DELETE /nodes/{node}/qemu/{vmid}  ·  DELETE /nodes/{node}/lxc/{vmid}
func (c *Client) DeleteGuest(ctx context.Context, node string, kind GuestType, vmid int, o DeleteOptions) (string, error) {
	e := epQemuDelete
	if kind == TypeLXC {
		e = epLXCDelete
	}
	var upid string
	if err := c.del(ctx, e, []string{node, strconv.Itoa(vmid)}, o.Values(kind), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// UpdateGuestConfig changes a guest's configuration.
//
// PUT /nodes/{node}/qemu/{vmid}/config  ·  PUT /nodes/{node}/lxc/{vmid}/config
func (c *Client) UpdateGuestConfig(ctx context.Context, node string, kind GuestType, vmid int, params url.Values) (string, error) {
	// This endpoint answers a UPID when the guest is running and an empty body
	// when it is not: a configuration change on a stopped VM is synchronous.
	// The pipeline handles both, which is why Write returns a string rather
	// than a task.
	e := epQemuUpdate
	if kind == TypeLXC {
		e = epLXCUpdate
	}
	var upid string
	if err := c.post(ctx, e, []string{node, strconv.Itoa(vmid)}, params, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// CreatePath renders the creation path, for --dry-run.
func CreatePath(kind GuestType, node string) string {
	if kind == TypeLXC {
		return epLXCCreate.Path(node)
	}
	return epQemuCreate.Path(node)
}

// ConfigPath renders the configuration path, for --dry-run.
func ConfigPath(kind GuestType, node string, vmid int) string {
	if kind == TypeLXC {
		return epLXCUpdate.Path(node, strconv.Itoa(vmid))
	}
	return epQemuUpdate.Path(node, strconv.Itoa(vmid))
}

// DeletePath renders the deletion path, for --dry-run.
func DeletePath(kind GuestType, node string, vmid int) string {
	if kind == TypeLXC {
		return epLXCDelete.Path(node, strconv.Itoa(vmid))
	}
	return epQemuDelete.Path(node, strconv.Itoa(vmid))
}
