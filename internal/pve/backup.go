package pve

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupMode is how vzdump reaches a consistent state before copying.
//
// The choice is not a performance setting, it is a consistency setting:
//
//   - stop      the guest is shut down. Total consistency, total downtime.
//   - suspend   the guest is frozen while the copy starts. In between.
//   - snapshot  the guest keeps running. Consistency at the BLOCK level only —
//     the archive holds what the disk contained at one instant, which
//     is not the same as what the application had finished writing.
//     A database can restore corrupt from a backup whose exitstatus
//     was OK.
type BackupMode string

const (
	ModeSnapshot BackupMode = "snapshot"
	ModeSuspend  BackupMode = "suspend"
	ModeStop     BackupMode = "stop"
)

// VZDumpOptions are the parameters of a backup.
//
// Schema verified with `pvesh usage /nodes/pve/vzdump -v` on the lab node.
type VZDumpOptions struct {
	// VMIDs is empty when All is set.
	VMIDs   []int
	All     bool
	Storage string
	Mode    BackupMode
	// Compress is "zstd", "gzip", "lzo" or "0" — where "0" means NO compression
	// rather than a level of zero.
	Compress string
	Notes    string
	// Prune applies the storage's retention policy after the backup, deleting
	// older archives. Off by default here, unlike the API: see Values.
	Prune bool
}

// Values renders the options as the payload that will be sent.
func (o VZDumpOptions) Values() url.Values {
	v := url.Values{}
	if o.All {
		v.Set("all", "1")
	} else {
		ids := make([]string, 0, len(o.VMIDs))
		for _, id := range o.VMIDs {
			ids = append(ids, strconv.Itoa(id))
		}
		v.Set("vmid", strings.Join(ids, ","))
	}
	if o.Storage != "" {
		v.Set("storage", o.Storage)
	}
	if o.Mode != "" {
		v.Set("mode", string(o.Mode))
	}
	if o.Compress != "" {
		v.Set("compress", o.Compress)
	}
	if o.Notes != "" {
		v.Set("notes-template", o.Notes)
	}

	// remove=1 is the API's default, and it means "apply the storage retention
	// policy" — that is, delete older archives nobody asked to delete. A backup
	// command that silently prunes is a backup command that can destroy the
	// only copy of something. It also needs Datastore.Allocate, which a
	// least-privilege token does not have, so the safe default is also the one
	// that works.
	if o.Prune {
		v.Set("remove", "1")
	} else {
		v.Set("remove", "0")
	}
	return v
}

// Backup starts a vzdump and returns the UPID of the task.
//
// The task is LONG — minutes for a few gigabytes — which is what makes this the
// first real test of the poller written at PVX-019.
//
// POST /nodes/{node}/vzdump
func (c *Client) Backup(ctx context.Context, node string, o VZDumpOptions) (string, error) {
	var upid string
	if err := c.post(ctx, epVZDump, []string{node}, o.Values(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// Archive is one backup volume, with what an operator actually needs to decide
// whether it is worth restoring.
type Archive struct {
	Volume
	// Storage is filled by pvecli: the content listing does not repeat it, but
	// a listing spanning several storages is unreadable without it.
	Storage string `json:"storage"`
}

// Age is how old the archive is. It IS the effective RPO: whatever was written
// since is not in it.
func (a Archive) Age(now time.Time) time.Duration {
	if a.CTime == 0 {
		return 0
	}
	return now.Sub(time.Unix(a.CTime, 0))
}

// Backups lists the archives held by a storage, most recent first.
//
// GET /nodes/{node}/storage/{storage}/content?content=backup
func (c *Client) Backups(ctx context.Context, node, storage string) ([]Archive, error) {
	volumes, err := c.StorageContent(ctx, node, storage, "backup")
	if err != nil {
		return nil, err
	}
	out := make([]Archive, 0, len(volumes))
	for _, v := range volumes {
		out = append(out, Archive{Volume: v, Storage: storage})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CTime > out[j].CTime })
	return out, nil
}

// BackupStorages returns the storages of a node that accept backups. Asking
// every storage for its backups earns a 400 from the ones that hold none by
// design.
func (c *Client) BackupStorages(ctx context.Context, node string) ([]Storage, error) {
	storages, err := c.Storages(ctx, node)
	if err != nil {
		return nil, err
	}
	var out []Storage
	for _, s := range storages {
		if s.Accepts("backup") && s.Enabled == 1 {
			out = append(out, s)
		}
	}
	return out, nil
}

// ArchiveGuestType reads the guest family out of a backup volid.
//
// "local:backup/vzdump-qemu-212-2026_07_31-20_45_02.vma.zst" → TypeQEMU. The
// archive already knows what it holds; asking the operator to restate it with a
// flag would only create a way to get it wrong.
func ArchiveGuestType(volid string) (GuestType, bool) {
	switch {
	case strings.Contains(volid, "vzdump-qemu-"):
		return TypeQEMU, true
	case strings.Contains(volid, "vzdump-lxc-"), strings.Contains(volid, "vzdump-openvz-"):
		return TypeLXC, true
	default:
		return "", false
	}
}

// ArchiveVMID reads the original vmid out of a backup volid, for `backup ls
// --check`. The content listing carries `vmid` too, but not on every storage
// type.
func ArchiveVMID(volid string) (int, bool) {
	for _, marker := range []string{"vzdump-qemu-", "vzdump-lxc-", "vzdump-openvz-"} {
		_, rest, found := strings.Cut(volid, marker)
		if !found {
			continue
		}
		id, _, _ := strings.Cut(rest, "-")
		if n, err := strconv.Atoi(id); err == nil {
			return n, true
		}
	}
	return 0, false
}

// RestoreOptions are the parameters of a restoration.
//
// There is no restore endpoint: a restoration is a CREATION carrying an
// archive. POST /nodes/{node}/qemu accepts `archive`, and `force` is declared
// by the schema as requiring it.
type RestoreOptions struct {
	Archive string
	Storage string
	// Overwrite maps to `force`: it allows the restoration to replace an
	// existing guest. Without it, PVE refuses — and so does pvecli, earlier.
	Overwrite bool
	Start     bool
}

// Values renders the options as the payload that will be sent.
func (o RestoreOptions) Values() url.Values {
	v := url.Values{}
	v.Set("archive", o.Archive)
	if o.Storage != "" {
		v.Set("storage", o.Storage)
	}
	if o.Overwrite {
		v.Set("force", "1")
	}
	if o.Start {
		v.Set("start", "1")
	}
	return v
}

// Restore recreates a guest from a backup archive and returns the UPID.
//
// POST /nodes/{node}/qemu  ·  POST /nodes/{node}/lxc
func (c *Client) Restore(ctx context.Context, node string, kind GuestType, vmid int, o RestoreOptions) (string, error) {
	return c.CreateGuest(ctx, node, kind, vmid, o.Values())
}

// BackupPath renders the vzdump path, for --dry-run.
func BackupPath(node string) string { return epVZDump.Path(node) }

// FormatAge renders a duration the way an operator reads an RPO.
func FormatAge(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d j", int(d.Hours()/24))
	}
}
