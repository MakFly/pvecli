package pve

import (
	"context"
	"net/url"
	"strconv"
)

// GuestAction is one of the state transitions PVE exposes.
//
// start and stop are not opposites of shutdown and reboot:
//
//   - stop cuts power. Instant, and the guest's filesystem finds out afterwards.
//   - shutdown asks the guest to leave via ACPI. It needs a cooperating OS, and
//     it can time out.
//
// Confusing the two is the most common cause of corruption in a homelab, which
// is why they are separate endpoints here as they are in the API.
type GuestAction string

const (
	ActionStart    GuestAction = "start"
	ActionStop     GuestAction = "stop"
	ActionShutdown GuestAction = "shutdown"
	ActionReboot   GuestAction = "reboot"
	ActionReset    GuestAction = "reset"
	ActionSuspend  GuestAction = "suspend"
	ActionResume   GuestAction = "resume"
)

// TargetStatus is the state a successful action leaves the guest in, or "" when
// the action has no single target state.
func (a GuestAction) TargetStatus() string {
	switch a {
	case ActionStart, ActionResume, ActionReboot, ActionReset:
		return "running"
	case ActionStop, ActionShutdown:
		return "stopped"
	default:
		return ""
	}
}

// SetGuestStatus asks the node for a state transition and returns the UPID of
// the task it queued.
//
// POST /nodes/{node}/qemu/{vmid}/status/{action}
// POST /nodes/{node}/lxc/{vmid}/status/{action}
func (c *Client) SetGuestStatus(ctx context.Context, node string, kind GuestType, vmid int, action GuestAction, params url.Values) (string, error) {
	e := epQemuAction
	if kind == TypeLXC {
		e = epLXCAction
	}

	var upid string
	if err := c.post(ctx, e, []string{node, strconv.Itoa(vmid), string(action)}, params, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// StatusPath renders the path a status change would use, for --dry-run.
func StatusPath(kind GuestType, node string, vmid int, action GuestAction) string {
	e := epQemuAction
	if kind == TypeLXC {
		e = epLXCAction
	}
	return e.Path(node, strconv.Itoa(vmid), string(action))
}
