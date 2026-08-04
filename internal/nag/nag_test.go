package nag

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeNode is a stand-in for the node: it holds a proxmoxlib.js in memory and
// runs the real scripts against it with /bin/sh. Nothing is mocked but the
// transport, so the sed expressions, the grep tests and the `set -eu` behaviour
// are all genuinely exercised.
type fakeNode struct {
	t       *testing.T
	dir     string
	restart int
}

func newFakeNode(t *testing.T, content string) *fakeNode {
	t.Helper()
	n := &fakeNode{t: t, dir: t.TempDir()}
	if content != "" {
		writeFile(t, n.dir+"/proxmoxlib.js", content)
	}
	// Stubs for the two commands the scripts call on a real node.
	writeExec(t, n.dir+"/pveversion", "#!/bin/sh\necho 'pve-manager/9.2.6/7f8d010005bd72cb'\n")
	writeExec(t, n.dir+"/systemctl", "#!/bin/sh\necho \"$@\" >> "+n.dir+"/restarts\n")
	return n
}

// runner rewrites the hardcoded File path to the temporary one, then runs the
// script for real.
func (n *fakeNode) runner() Runner {
	return func(ctx context.Context, script string) (string, error) {
		script = strings.ReplaceAll(script, File, n.dir+"/proxmoxlib.js")
		c := exec.CommandContext(ctx, "/bin/sh", "-s")
		c.Stdin = strings.NewReader(script)
		c.Env = append(c.Environ(), "PATH="+n.dir+":/usr/bin:/bin")
		out, err := c.Output()
		return string(out), err
	}
}

func (n *fakeNode) js() string     { return readFile(n.t, n.dir+"/proxmoxlib.js") }
func (n *fakeNode) restarts() bool { return fileExists(n.dir + "/restarts") }

// pristine is the real shape of the function on PVE 9.2.6, including the two
// legitimate `orig_cmd();` calls that make any count-based detection wrong.
const pristine = `Ext.define('Proxmox.Utils', {
    checked_command: function (orig_cmd) {
        Proxmox.Utils.API2Request({
            url: '/nodes/localhost/subscription',
            success: function (response, opts) {
                let res = response.result;
                if (
                    res === null ||
                    res.data.status.toLowerCase() !== 'active'
                ) {
                    Ext.Msg.show({ title: gettext('No valid subscription') });
                } else {
                    orig_cmd();
                }
            },
            failure: function () { orig_cmd(); },
        });
    },
});
`

func TestStatusOnPristineFile(t *testing.T) {
	n := newFakeNode(t, pristine)

	rep, err := Status(context.Background(), n.runner())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if rep.State != StateOn {
		t.Errorf("state = %q, attendu %q", rep.State, StateOn)
	}
	if rep.Changed {
		t.Error("status ne doit jamais rapporter changed=true")
	}
	if rep.Version != "pve-manager/9.2.6/7f8d010005bd72cb" {
		t.Errorf("version = %q", rep.Version)
	}
	if n.restarts() {
		t.Error("status a redémarré pveproxy — il doit être en lecture seule")
	}
}

// The regression this whole package is built around: `grep -c "orig_cmd();"`
// returns 2 on the pristine file. A detection based on it would report an
// unpatched node as patched.
func TestPristineFileAlreadyContainsOrigCmdCalls(t *testing.T) {
	if got := strings.Count(pristine, "orig_cmd();"); got != 2 {
		t.Fatalf("le fichier vierge contient %d occurrences de « orig_cmd(); », attendu 2", got)
	}
}

func TestOffThenOnRoundTrips(t *testing.T) {
	n := newFakeNode(t, pristine)

	rep, err := Off(context.Background(), n.runner())
	if err != nil {
		t.Fatalf("Off: %v", err)
	}
	if rep.State != StateOff || !rep.Changed {
		t.Fatalf("Off a rendu state=%q changed=%v", rep.State, rep.Changed)
	}
	if !strings.Contains(n.js(), patched) {
		t.Fatalf("le patch n'est pas dans le fichier :\n%s", n.js())
	}
	if !n.restarts() {
		t.Error("pveproxy n'a pas été redémarré après un patch effectif")
	}

	rep, err = On(context.Background(), n.runner())
	if err != nil {
		t.Fatalf("On: %v", err)
	}
	if rep.State != StateOn || !rep.Changed {
		t.Fatalf("On a rendu state=%q changed=%v", rep.State, rep.Changed)
	}
	// The exact inverse: byte for byte the file it started from. This is what
	// makes the absence of a .bak defensible.
	if n.js() != pristine {
		t.Errorf("le retrait n'est pas l'inverse exact de l'insertion :\n%s", n.js())
	}
}

func TestOffIsIdempotent(t *testing.T) {
	n := newFakeNode(t, pristine)
	ctx := context.Background()

	if _, err := Off(ctx, n.runner()); err != nil {
		t.Fatalf("premier Off: %v", err)
	}
	after := n.js()

	rep, err := Off(ctx, n.runner())
	if err != nil {
		t.Fatalf("second Off: %v", err)
	}
	if rep.Changed {
		t.Error("le second Off se déclare modifiant alors que le nœud était déjà patché")
	}
	if n.js() != after {
		t.Error("le second Off a réécrit le fichier — l'injection s'imbrique")
	}
	if strings.Count(n.js(), marker) != 1 {
		t.Errorf("marqueur présent %d fois, attendu 1", strings.Count(n.js(), marker))
	}
}

func TestOnIsIdempotent(t *testing.T) {
	n := newFakeNode(t, pristine)

	rep, err := On(context.Background(), n.runner())
	if err != nil {
		t.Fatalf("On: %v", err)
	}
	if rep.State != StateOn || rep.Changed {
		t.Errorf("On sur un nœud non patché : state=%q changed=%v", rep.State, rep.Changed)
	}
	if n.restarts() {
		t.Error("pveproxy redémarré alors qu'il n'y avait rien à retirer")
	}
}

// A PVE version that moved the code must produce a refusal, never a blind edit.
func TestUnknownFileIsRefusedNotPatched(t *testing.T) {
	const moved = "Ext.define('Proxmox.Utils', { something_else: function () {} });\n"
	n := newFakeNode(t, moved)

	rep, err := Off(context.Background(), n.runner())
	if err == nil {
		t.Fatal("Off a accepté un fichier dont l'ancrage est absent")
	}
	if rep.State != StateUnknown {
		t.Errorf("state = %q, attendu %q", rep.State, StateUnknown)
	}
	if n.js() != moved {
		t.Error("le fichier a été modifié malgré l'ancrage absent")
	}
	if !strings.Contains(err.Error(), "9.2.6") {
		t.Errorf("l'erreur doit citer la version du nœud, elle dit : %v", err)
	}
}

func TestAbsentFileIsReported(t *testing.T) {
	n := newFakeNode(t, "")

	rep, err := Status(context.Background(), n.runner())
	if err == nil {
		t.Fatal("un proxmoxlib.js absent doit être une erreur")
	}
	if rep.State != StateAbsent {
		t.Errorf("state = %q, attendu %q", rep.State, StateAbsent)
	}
}

// sedPattern exists because of a bug that was written once already: `/*` in a
// basic regular expression means "zero or more slashes", so the unescaped
// marker does not match the text it was copied from.
func TestSedPatternEscapesRegexpMetacharacters(t *testing.T) {
	got := sedPattern(injected)
	if !strings.Contains(got, `/\*`) || !strings.Contains(got, `\*/`) {
		t.Errorf("les astérisques du marqueur ne sont pas échappées : %q", got)
	}
	if strings.Contains(sedPattern("a.b"), "a.b") {
		t.Error("le point n'est pas échappé")
	}
}

func TestSedReplacementEscapesAmpersand(t *testing.T) {
	if got := sedReplacement("a&b"); got != `a\&b` {
		t.Errorf("sedReplacement(%q) = %q", "a&b", got)
	}
}

func TestParseIgnoresUnknownKeys(t *testing.T) {
	rep := parse("version=x\nfuture_key=1\nstate=off\nchanged=yes\n")
	if rep.State != StateOff || !rep.Changed || rep.Version != "x" {
		t.Errorf("parse = %+v", rep)
	}
}

// A runner failure must surface, and must not be masked by the state machine.
func TestRunnerErrorIsReturned(t *testing.T) {
	boom := errors.New("connexion refusée")
	failing := func(context.Context, string) (string, error) { return "", boom }

	if _, err := Status(context.Background(), failing); !errors.Is(err, boom) {
		t.Errorf("err = %v, attendu %v", err, boom)
	}
}

func TestTargetJoinsUserAndHost(t *testing.T) {
	if got := (SSH{Host: "192.168.1.23", User: "root"}).Target(); got != "root@192.168.1.23" {
		t.Errorf("Target() = %q", got)
	}
}
