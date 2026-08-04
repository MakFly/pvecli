// Package nag neutralises the "no valid subscription" dialog of the Proxmox VE
// web interface, and can put it back.
//
// It is the one corner of pvecli that does not speak to /api2/json. The dialog
// is produced by a JavaScript file served from the node's own filesystem —
// /usr/share/javascript/proxmox-widget-toolkit/proxmoxlib.js — and no REST
// endpoint reaches it, whatever privileges the token carries. So this package
// runs a shell script on the node instead, over the operator's existing ssh.
//
// The design rests on three decisions, each taken against a specific way this
// could rot:
//
//   - Detection uses an explicit marker, never a count. The naive check that
//     circulates online, `grep -c "orig_cmd();"`, returns 2 on a pristine file:
//     those are the function's own legitimate calls. Any count-based probe
//     therefore reports "already patched" about a node that is not.
//   - No .bak file is kept. The patch is a pure textual insertion, so removing
//     it is its exact inverse. A backup would be worse than nothing: after an
//     `apt upgrade` replaces the file, restoring the stale copy would downgrade
//     the widget toolkit behind the operator's back.
//   - No APT hook is installed. Persisting the patch across upgrades means
//     leaving a script on the node that silently rewrites package-owned files
//     forever. `nag status` tells the truth after an upgrade; `nag off` replays
//     in a second. That is the reversible trade.
package nag

import (
	"context"
	"fmt"
	"strings"
)

// File is the widget toolkit served to every browser that opens the web UI.
const File = "/usr/share/javascript/proxmox-widget-toolkit/proxmoxlib.js"

// marker is what makes the patch recognisable, removable, and impossible to
// confuse with the file's own code.
const marker = "/* pvecli:nag-off */"

// anchor is the exact opening line of the function that raises the dialog,
// verified on PVE 9.2.6. It is matched as a fixed string, never as a regexp:
// a loose match on a 200 kB file is how a "patch" becomes a corruption.
const anchor = "checked_command: function (orig_cmd) {"

// patched is the anchor with the early return injected. Calling orig_cmd()
// before returning matters: the dialog is suppressed, but the command the user
// actually asked for still runs.
const patched = anchor + injected

// injected is what Off adds and On removes, exactly.
const injected = " orig_cmd(); return; " + marker

// Runner executes a POSIX shell script on the node and returns its stdout.
//
// A function rather than an ssh client, so the whole state machine below is
// testable without a node — and so an operator who reaches their hypervisor
// some other way is not locked out of this command.
type Runner func(ctx context.Context, script string) (stdout string, err error)

// State is what the node's proxmoxlib.js currently says.
type State string

const (
	// StateOn: the subscription check is intact, the dialog appears.
	StateOn State = "active"
	// StateOff: pvecli's patch is present, the dialog is suppressed.
	StateOff State = "neutralisée"
	// StateUnknown: the file exists but carries neither the anchor nor the
	// marker. Almost always a newer PVE that moved the code — reported, never
	// guessed at.
	StateUnknown State = "inconnu"
	// StateAbsent: no proxmoxlib.js. Not a Proxmox node, or not this path.
	StateAbsent State = "absent"
)

// Report is the outcome of one operation.
type Report struct {
	State   State  `json:"state"`
	Changed bool   `json:"changed"`
	File    string `json:"file"`
	Version string `json:"version,omitempty"`
}

// Status reads the node without touching it.
func Status(ctx context.Context, run Runner) (Report, error) {
	return apply(ctx, run, statusScript())
}

// Off suppresses the dialog. Idempotent: a node already patched is reported as
// unchanged, not patched twice.
func Off(ctx context.Context, run Runner) (Report, error) {
	return apply(ctx, run, offScript())
}

// On restores the subscription check, by removing exactly what Off inserted.
func On(ctx context.Context, run Runner) (Report, error) {
	return apply(ctx, run, onScript())
}

// apply runs a script and turns its key=value output into a Report.
func apply(ctx context.Context, run Runner, script string) (Report, error) {
	out, err := run(ctx, script)
	rep := parse(out)
	rep.File = File
	if err != nil {
		return rep, err
	}
	switch rep.State {
	case StateAbsent:
		return rep, fmt.Errorf(
			"%s est introuvable sur le nœud.\n\n"+
				"Ce chemin appartient au paquet « proxmox-widget-toolkit » : soit la cible n'est pas\n"+
				"un nœud PVE, soit le paquet n'y est pas installé", File)
	case StateUnknown:
		return rep, fmt.Errorf(
			"le fichier existe mais ne contient ni le repère de pvecli ni le code attendu :\n"+
				"  %s\n\n"+
				"Cette version de PVE (%s) a vraisemblablement déplacé la vérification d'abonnement.\n"+
				"Je ne patche pas à l'aveugle un fichier de 200 ko. Ouvre-le et vérifie où la\n"+
				"fonction « checked_command » a atterri, plutôt que de récupérer une interface cassée",
			anchor, rep.Version)
	}
	return rep, nil
}

// parse reads the `clé=valeur` lines the scripts emit. Unknown keys are
// ignored, so a future script can add one without breaking an older binary.
func parse(out string) Report {
	rep := Report{State: StateUnknown}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "state":
			switch value {
			case "on":
				rep.State = StateOn
			case "off":
				rep.State = StateOff
			case "absent":
				rep.State = StateAbsent
			default:
				rep.State = StateUnknown
			}
		case "changed":
			rep.Changed = value == "yes"
		case "version":
			rep.Version = value
		}
	}
	return rep
}

// shQuote wraps a literal for single-quoted shell context.
//
// Every value these scripts interpolate is a constant of this package, not user
// input. But the scripts are assembled by concatenation and handed to a root
// shell, and "it is a constant today" is not a property that survives edits.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sedPattern escapes a literal so sed's basic regular expressions match it
// character for character.
//
// This is not defensive noise, it is a bug that was already written once: the
// marker contains `/*`, and in a BRE `*` quantifies whatever precedes it. The
// unescaped pattern `/* pvecli:nag-off */` therefore means "zero or more
// slashes, then a space…", which does not match the very text it was copied
// from. The `on` path silently removed nothing.
func sedPattern(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`.`, `\.`,
		`*`, `\*`,
		`[`, `\[`,
		`]`, `\]`,
		`^`, `\^`,
		`$`, `\$`,
		`|`, `\|`, // the delimiter used below
	)
	return r.Replace(s)
}

// sedReplacement escapes the right-hand side, where `&` means "the whole match"
// and would otherwise duplicate the anchor into the file.
func sedReplacement(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`&`, `\&`,
		`|`, `\|`,
	)
	return r.Replace(s)
}

// sedExpr builds one `s|…|…|` substitution, already shell-quoted.
func sedExpr(from, to string) string {
	return shQuote("s|" + sedPattern(from) + "|" + sedReplacement(to) + "|")
}

// preamble is shared by the three scripts: strict mode, the target file, the
// node's version, and the early exit when there is nothing to look at.
//
// `set -eu` matters here. Without it a failing sed would let the script run on
// to print `state=off` about a file it never touched, and a report that lies is
// worse than an error.
func preamble() string {
	return "set -eu\n" +
		"f=" + shQuote(File) + "\n" +
		`if [ ! -f "$f" ]; then echo state=absent; echo changed=no; exit 0; fi` + "\n" +
		`v=$(pveversion 2>/dev/null | head -1 || true); echo "version=$v"` + "\n"
}

// has is a grep test written as a full `if`, never as `grep … && …`.
// Under `set -e`, a trailing `&&` whose left side fails makes the whole command
// non-zero and kills the script — the classic way these one-liners break.
func has(needle string) string {
	return "grep -qF " + shQuote(needle) + ` "$f"`
}

func statusScript() string {
	return preamble() +
		"echo changed=no\n" +
		"if " + has(marker) + "; then echo state=off; exit 0; fi\n" +
		"if " + has(anchor) + "; then echo state=on; exit 0; fi\n" +
		"echo state=unknown\n"
}

func offScript() string {
	return preamble() +
		// Already patched: say so and stop. Running sed twice would nest the
		// injection into a file neither `on` nor a human could undo.
		"if " + has(marker) + "; then echo state=off; echo changed=no; exit 0; fi\n" +
		"if ! " + has(anchor) + "; then echo state=unknown; echo changed=no; exit 0; fi\n" +
		"sed -i " + sedExpr(anchor, patched) + ` "$f"` + "\n" +
		// Verify before restarting pveproxy: a claimed patch that did not land
		// is exactly the failure the marker exists to make impossible.
		"if ! " + has(marker) + "; then\n" +
		`  echo "le patch n'a pas pris : sed a rendu 0 mais le repère est absent." >&2` + "\n" +
		"  echo state=unknown; echo changed=no; exit 1\n" +
		"fi\n" +
		"systemctl restart pveproxy.service\n" +
		"echo state=off\necho changed=yes\n"
}

func onScript() string {
	return preamble() +
		"if ! " + has(marker) + "; then echo state=on; echo changed=no; exit 0; fi\n" +
		"sed -i " + sedExpr(injected, "") + ` "$f"` + "\n" +
		"if " + has(marker) + "; then\n" +
		`  echo "le repère de pvecli est toujours là après retrait — fichier modifié à la main ?" >&2` + "\n" +
		"  echo state=unknown; echo changed=no; exit 1\n" +
		"fi\n" +
		"systemctl restart pveproxy.service\n" +
		"echo state=on\necho changed=yes\n"
}
