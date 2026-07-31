package iac

import (
	"strconv"
	"strings"
)

// Binaries Ansible ships. Named here rather than inline for the same reason PVE
// endpoints are: a string literal at a call site is a string nothing checks.
const (
	AnsibleBin         = "ansible"
	AnsiblePlaybookBin = "ansible-playbook"
)

// Recap is one line of Ansible's PLAY RECAP.
//
// It is the only machine-readable thing a default ansible-playbook run emits,
// and it is what makes idempotence measurable rather than declared. Ansible has
// a JSON callback plugin, but enabling it changes the output the operator sees
// — and this command's contract is that it does not rewrite what Ansible says.
type Recap struct {
	Host        string `json:"host"`
	OK          int    `json:"ok"`
	Changed     int    `json:"changed"`
	Unreachable int    `json:"unreachable"`
	Failed      int    `json:"failed"`
}

// ParseRecap reads the PLAY RECAP block out of a run's output.
//
// The format has been stable across Ansible major versions:
//
//	lab-app-01 : ok=7    changed=3    unreachable=0    failed=0    skipped=1 …
//
// Anything before the "PLAY RECAP" banner is ignored, so a task that happens to
// print "changed=" in its own output cannot be mistaken for the summary.
func ParseRecap(out string) []Recap {
	lines := strings.Split(out, "\n")

	start := -1
	for i, l := range lines {
		if strings.Contains(l, "PLAY RECAP") {
			start = i + 1
		}
	}
	if start < 0 {
		return nil
	}

	var recaps []Recap
	for _, l := range lines[start:] {
		host, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		host = strings.TrimSpace(host)
		if host == "" || !strings.Contains(rest, "ok=") {
			continue
		}

		r := Recap{Host: host}
		for _, field := range strings.Fields(rest) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				continue
			}
			switch key {
			case "ok":
				r.OK = n
			case "changed":
				r.Changed = n
			case "unreachable":
				r.Unreachable = n
			case "failed":
				r.Failed = n
			}
		}
		recaps = append(recaps, r)
	}
	return recaps
}

// TotalChanged sums what the run actually modified.
//
// This is the number that decides whether a playbook is idempotent. Not the
// exit code: a second run that changes everything again exits 0 and looks like
// a success. Idempotence is a MEASUREMENT, and this is the measurement.
func TotalChanged(recaps []Recap) int {
	total := 0
	for _, r := range recaps {
		total += r.Changed
	}
	return total
}

// ChangedHosts names which hosts changed, so a failed idempotence check points
// at something instead of just failing.
func ChangedHosts(recaps []Recap) []string {
	var out []string
	for _, r := range recaps {
		if r.Changed > 0 {
			out = append(out, r.Host+" ("+strconv.Itoa(r.Changed)+")")
		}
	}
	return out
}

// UnreachableHosts names the hosts a playbook run could not reach.
func UnreachableHosts(recaps []Recap) []string {
	var out []string
	for _, r := range recaps {
		if r.Unreachable > 0 || r.Failed > 0 {
			out = append(out, r.Host)
		}
	}
	return out
}

// Ping is one host's answer to `ansible -m ping`.
type Ping struct {
	Host   string
	Result string
}

// OK reports whether the host answered.
func (p Ping) OK() bool { return p.Result == "SUCCESS" }

// ParsePing reads the output of an AD-HOC ansible run.
//
// It needs its own parser because an ad-hoc run prints no PLAY RECAP at all —
// that banner belongs to ansible-playbook. Feeding this output to ParseRecap
// returns an empty slice, which reads as "zero hosts answered" and, worse,
// as "no host failed". The first version of the ping pre-check did exactly
// that and cheerfully announced « ✓ 0 hôte(s) répondent ».
//
// The ad-hoc format is one line per host:
//
//	lab-app-01 | SUCCESS => { …
//	web        | UNREACHABLE! => { …
func ParsePing(out string) []Ping {
	var pings []Ping
	for _, line := range strings.Split(out, "\n") {
		host, rest, ok := strings.Cut(line, " | ")
		if !ok {
			continue
		}
		verdict, _, ok := strings.Cut(rest, " =>")
		if !ok {
			continue
		}
		host = strings.TrimSpace(host)
		// A host name never contains a space; a wrapped log line might.
		if host == "" || strings.ContainsAny(host, " \t") {
			continue
		}
		pings = append(pings, Ping{
			Host:   host,
			Result: strings.TrimSuffix(strings.TrimSpace(verdict), "!"),
		})
	}
	return pings
}
