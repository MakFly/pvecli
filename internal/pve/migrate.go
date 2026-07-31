package pve

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// MigratePrecheck is the answer of GET …/migrate — « Get preconditions for
// migration ». Reading it before writing is the whole story of PVX-052: the
// command is trivial, the conditions are not.
type MigratePrecheck struct {
	Running        bool
	AllowedNodes   []string
	NotAllowedNode map[string]string
	LocalDisks     []LocalDisk
	LocalResources []string
}

// LocalDisk is one disk that lives on a storage the target cannot reach. Each
// of these has to be copied over the wire, which is what --with-local-disks
// asks for — and what turns a two-second migration into a long one.
type LocalDisk struct {
	VolID      string `json:"volid"`
	DriveName  string `json:"drivename,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Shared     int    `json:"shared,omitempty"`
	CDROM      int    `json:"cdrom,omitempty"`
	IsCloudnit int    `json:"is_cloudinit,omitempty"`
}

// UnmarshalJSON reads both spellings the API uses for the same fields.
//
// QEMU answers allowed_nodes / not_allowed_nodes / local_disks, with
// underscores. LXC answers allowed-nodes / not-allowed-nodes, with hyphens.
// One struct reused for both decodes silently empty on one of the two, and an
// empty allowed_nodes is exactly what « you cannot migrate » looks like — so
// the bug would hide behind a plausible answer. Verified against the lab on
// VM 211 and container 120.
func (m *MigratePrecheck) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Running int `json:"running"`

		AllowedNodesQemu []string `json:"allowed_nodes"`
		AllowedNodesLXC  []string `json:"allowed-nodes"`

		NotAllowedQemu map[string]string `json:"not_allowed_nodes"`
		NotAllowedLXC  map[string]string `json:"not-allowed-nodes"`

		LocalDisks     []LocalDisk `json:"local_disks"`
		LocalResources []string    `json:"local_resources"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}

	m.Running = wire.Running == 1
	m.AllowedNodes = wire.AllowedNodesQemu
	if m.AllowedNodes == nil {
		m.AllowedNodes = wire.AllowedNodesLXC
	}
	m.NotAllowedNode = wire.NotAllowedQemu
	if m.NotAllowedNode == nil {
		m.NotAllowedNode = wire.NotAllowedLXC
	}
	m.LocalDisks = wire.LocalDisks
	m.LocalResources = wire.LocalResources
	return nil
}

// MovableDisks keeps the disks that would really have to travel: a shared
// volume does not, and a CD-ROM is not a disk to copy.
func (m MigratePrecheck) MovableDisks() []LocalDisk {
	var out []LocalDisk
	for _, d := range m.LocalDisks {
		if d.Shared == 1 || (d.CDROM == 1 && d.IsCloudnit == 0) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// MigratePreconditions asks the node what a migration would need.
//
// GET /nodes/{node}/qemu/{vmid}/migrate  ·  GET /nodes/{node}/lxc/{vmid}/migrate
func (c *Client) MigratePreconditions(ctx context.Context, node string, kind GuestType, vmid int, target string) (*MigratePrecheck, error) {
	e := epQemuMigratePre
	if kind == TypeLXC {
		e = epLXCMigratePre
	}
	var query url.Values
	if target != "" {
		query = url.Values{"target": {target}}
	}

	var out MigratePrecheck
	if err := c.get(ctx, e, []string{node, strconv.Itoa(vmid)}, query, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MigrateOptions are the parameters of a migration.
//
// Schema read on the node with `pvesh usage /nodes/pve/qemu/211/migrate -v`.
// The QEMU and LXC endpoints do NOT take the same set: a container has no live
// migration, it is restarted (`restart`, `timeout`), where a VM has `online`.
type MigrateOptions struct {
	Target string

	// Online asks for a live migration. Ignored on a stopped VM.
	Online bool
	// WithLocalDisks copies the disks the target cannot reach. Without shared
	// storage, this is the only way — and it is what costs the time.
	WithLocalDisks bool
	TargetStorage  string
	BWLimit        int

	// Restart is the LXC counterpart of Online: the container is stopped,
	// moved, and started again. There is no live migration for a container.
	Restart bool
	Timeout int
}

// Values renders the payload for a guest type, so --dry-run and the request
// cannot drift apart.
func (o MigrateOptions) Values(kind GuestType) url.Values {
	v := url.Values{"target": {o.Target}}
	if o.TargetStorage != "" {
		v.Set("targetstorage", o.TargetStorage)
	}
	if o.BWLimit > 0 {
		v.Set("bwlimit", strconv.Itoa(o.BWLimit))
	}

	if kind == TypeLXC {
		if o.Restart {
			v.Set("restart", "1")
		}
		if o.Timeout > 0 {
			v.Set("timeout", strconv.Itoa(o.Timeout))
		}
		return v
	}

	if o.Online {
		v.Set("online", "1")
	}
	if o.WithLocalDisks {
		v.Set("with-local-disks", "1")
	}
	return v
}

// MigrateGuest starts a migration and returns the UPID.
//
// POST /nodes/{node}/qemu/{vmid}/migrate  ·  POST /nodes/{node}/lxc/{vmid}/migrate
func (c *Client) MigrateGuest(ctx context.Context, node string, kind GuestType, vmid int, o MigrateOptions) (string, error) {
	e := epQemuMigrate
	if kind == TypeLXC {
		e = epLXCMigrate
	}
	var upid string
	if err := c.post(ctx, e, []string{node, strconv.Itoa(vmid)}, o.Values(kind), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// MigratePath renders the migration path, for --dry-run.
func MigratePath(kind GuestType, node string, vmid int) string {
	if kind == TypeLXC {
		return epLXCMigrate.Path(node, strconv.Itoa(vmid))
	}
	return epQemuMigrate.Path(node, strconv.Itoa(vmid))
}
