package pve

import (
	"fmt"
	"regexp"
	"strconv"
)

// UPID identifies one task on one node.
//
//	UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user>:
//	UPID:pve:000006FD:000043CD:6A6CAD90:vncshell::root@pam:
//
// The regexp below is transcribed from PVE::UPID::decode on the lab node
// (PVE 9.2.2), including its two oddities: pstart may be 8 *or 9* hex digits —
// nine covers twenty years of uptime — and the whole thing ends on a colon.
//
// The field that matters most is `node`. A UPID says where the task runs, which
// is what makes it possible to follow a job from anywhere without assuming it
// landed on the default node. Polling the wrong node is a 500 that reads like
// a vanished task.
type UPID struct {
	Raw string

	Node      string
	PID       int64
	PStart    int64
	StartTime int64
	Type      string
	ID        string
	User      string
}

var upidRe = regexp.MustCompile(
	`^UPID:([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?):([0-9A-Fa-f]{8}):([0-9A-Fa-f]{8,9}):([0-9A-Fa-f]{8}):([^:\s/]+):([^:\s/]*):([^:\s/]+):$`)

// ParseUPID decodes a UPID, or explains why it cannot.
func ParseUPID(s string) (*UPID, error) {
	m := upidRe.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("UPID illisible %q — format attendu :\n  UPID:<nœud>:<pid>:<pstart>:<starttime>:<type>:<id>:<utilisateur>:  (deux-points final compris)", s)
	}

	pid, _ := strconv.ParseInt(m[3], 16, 64)
	pstart, _ := strconv.ParseInt(m[4], 16, 64)
	start, _ := strconv.ParseInt(m[5], 16, 64)

	return &UPID{
		Raw:       s,
		Node:      m[1],
		PID:       pid,
		PStart:    pstart,
		StartTime: start,
		Type:      m[6],
		ID:        m[7],
		User:      m[8],
	}, nil
}

// String returns the UPID exactly as it arrived: it is an opaque identifier to
// every endpoint that consumes it, so a lossy round-trip would be a bug.
func (u *UPID) String() string { return u.Raw }

// IsUPID reports whether a mutation answered with a task id rather than a
// value. PVE mutations mostly do; a few answer synchronously, and those must
// not be polled.
func IsUPID(s string) bool { return upidRe.MatchString(s) }
