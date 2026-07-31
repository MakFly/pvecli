// Package output renders results for humans and for machines (PRD §7.4).
//
// Discipline it enforces: data goes to stdout, everything else — progress,
// warnings, confirmation prompts — goes to stderr, so that
// `pvecli vm ls -o json | jq` always works.
//
// The table|json|yaml renderers land with PVX-010; what lives here today is the
// handful of conversions every listing needs.
package output

import (
	"fmt"
	"time"
)

// Bytes renders a byte count in the unit a human reads at a glance. Proxmox
// answers in bytes everywhere, and nobody reads 33041162240.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// Uptime renders a duration in seconds as days/hours/minutes.
func Uptime(seconds int64) string {
	if seconds <= 0 {
		return "—"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dj %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// Ratio renders a 0..1 load as a percentage. Proxmox reports CPU usage as a
// ratio, not a percentage — reading it as one is a factor-100 mistake that
// looks plausible on an idle node.
func Ratio(r float64) string {
	return fmt.Sprintf("%.1f %%", r*100)
}

// Timestamp renders a Unix time as a short local date-time. PVE answers epoch
// seconds everywhere.
func Timestamp(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04")
}
