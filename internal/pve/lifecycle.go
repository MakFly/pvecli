package pve

import (
	"context"
	"net/url"
	"strconv"
)

// CloneOptions are the parameters of a clone.
//
// Schema verified in PVE::API2::Qemu::clone_vm on the lab node: newid, name,
// description, pool, snapname, storage, format, full, target. The LXC endpoint
// takes the same set with one difference, checked in `pvesh usage
// /nodes/pve/lxc/100/clone -v`: the new guest is named by `hostname`, not by
// `name`. Symmetry that stops one field short is exactly the kind of thing
// that gets written from memory and rejected by the node.
type CloneOptions struct {
	NewID       int
	Name        string
	Description string
	Pool        string
	Storage     string
	Target      string
	// Full asks for an independent copy. Left false, PVE makes a LINKED clone,
	// which shares the template's blocks — destroying the template then breaks
	// every clone made from it. The web interface hides this; the CLI does not.
	Full bool
}

// Values renders the options as the payload that will be sent, so --dry-run and
// the request cannot drift apart.
func (o CloneOptions) Values(kind GuestType) url.Values {
	v := url.Values{}
	v.Set("newid", strconv.Itoa(o.NewID))
	if o.Name != "" {
		if kind == TypeLXC {
			v.Set("hostname", o.Name)
		} else {
			v.Set("name", o.Name)
		}
	}
	if o.Description != "" {
		v.Set("description", o.Description)
	}
	if o.Pool != "" {
		v.Set("pool", o.Pool)
	}
	if o.Storage != "" {
		v.Set("storage", o.Storage)
	}
	if o.Target != "" {
		v.Set("target", o.Target)
	}
	if o.Full {
		v.Set("full", "1")
	}
	return v
}

// CloneGuest copies a guest and returns the UPID of the task.
//
// POST /nodes/{node}/qemu/{vmid}/clone  ·  POST /nodes/{node}/lxc/{vmid}/clone
func (c *Client) CloneGuest(ctx context.Context, node string, kind GuestType, vmid int, o CloneOptions) (string, error) {
	e := epQemuClone
	if kind == TypeLXC {
		e = epLXCClone
	}
	var upid string
	if err := c.post(ctx, e, []string{node, strconv.Itoa(vmid)}, o.Values(kind), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// TemplateVM converts a stopped guest into a template.
//
// POST /nodes/{node}/qemu/{vmid}/template
func (c *Client) TemplateVM(ctx context.Context, node string, vmid int) (string, error) {
	var upid string
	if err := c.post(ctx, epQemuTemplate, []string{node, strconv.Itoa(vmid)}, url.Values{}, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// ClonePath renders the clone path, for --dry-run.
func ClonePath(kind GuestType, node string, vmid int) string {
	if kind == TypeLXC {
		return epLXCClone.Path(node, strconv.Itoa(vmid))
	}
	return epQemuClone.Path(node, strconv.Itoa(vmid))
}

// TemplatePath renders the template path, for --dry-run.
func TemplatePath(node string, vmid int) string {
	return epQemuTemplate.Path(node, strconv.Itoa(vmid))
}
