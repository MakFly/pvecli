package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/config"
	"github.com/MakFly/pvecli/internal/testutil"
	"github.com/spf13/cobra"
)

// isolateCompletionCache keeps a test from reading — or poisoning — the
// developer's real cache, which is keyed by endpoint and would otherwise be
// shared with the live lab.
func isolateCompletionCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func completeArgs(t *testing.T, args ...string) (stdout, ours string) {
	t.Helper()
	out, errOut, _ := run(t, append([]string{cobra.ShellCompRequestCmd}, args...)...)
	return out, ourStderr(errOut)
}

// ourStderr drops the one line Cobra always writes itself — « Completion ended
// with directive: … » — so a test can assert on what PVECLI said. Cobra's line
// is part of its protocol and every shell integration discards stderr; ours
// would land in the middle of the prompt.
func ourStderr(raw string) string {
	var kept []string
	for _, line := range strings.Split(raw, "\n") {
		if line == "" || strings.HasPrefix(line, "Completion ended with directive:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// The whole contract of PVX-053: a completion never blocks and never speaks.
// Here the endpoint points at a closed port, so every call fails.
func TestCompletionStaysSilentWhenTheNodeIsUnreachable(t *testing.T) {
	isolateCompletionCache(t)

	// A server that is started and immediately closed gives an address that
	// refuses connections, which is the failure an operator actually meets.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	t.Setenv(config.EnvEndpoint, url)
	t.Setenv(config.EnvTokenID, "automation@pve!pvectl")
	t.Setenv(config.EnvTokenSecret, "s3cr3t")

	stdout, stderr := completeArgs(t, "vm", "show", "")

	if stderr != "" {
		t.Errorf("la complétion a écrit sur stderr : %q — cela atterrit au milieu de l'invite", stderr)
	}
	// Cobra always prints its directive line; what must not be there is a
	// candidate or an error.
	for _, line := range strings.Split(stdout, "\n") {
		if line != "" && !strings.HasPrefix(line, ":") {
			t.Errorf("candidat proposé alors que le nœud est injoignable : %q", line)
		}
	}
}

// No token at all is the other half of the same promise: a fresh install must
// not make the shell print an authentication error at every Tab.
func TestCompletionStaysSilentWithoutCredentials(t *testing.T) {
	isolateCompletionCache(t)
	t.Setenv(config.EnvEndpoint, "")
	t.Setenv(config.EnvTokenID, "")
	t.Setenv(config.EnvTokenSecret, "")

	stdout, stderr := completeArgs(t, "vm", "show", "")
	if stderr != "" {
		t.Errorf("stderr = %q, la complétion doit se taire", stderr)
	}
	if strings.Contains(stdout, "token") || strings.Contains(stdout, "endpoint") {
		t.Errorf("un message d'erreur a fui dans les candidats : %q", stdout)
	}
}

func TestCompletionOffersVMIDWithTheirName(t *testing.T) {
	isolateCompletionCache(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/resources": "cluster-resources-full.json",
	})
	point(t, srv.URL)

	stdout, _ := completeArgs(t, "vm", "show", "")

	if !strings.Contains(stdout, "\t") {
		t.Errorf("les VMID doivent porter le nom du guest en description : %q", stdout)
	}
	// A container must not be offered where a VM is expected: `vm show 120`
	// is a 404 the completion should never have suggested.
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "120\t") {
			t.Errorf("le conteneur 120 est proposé par « vm show » : %q", stdout)
		}
	}
}

func TestCompletionSeparatesTheTwoFamilies(t *testing.T) {
	isolateCompletionCache(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/resources": "cluster-resources-full.json",
	})
	point(t, srv.URL)

	stdout, _ := completeArgs(t, "lxc", "show", "")
	if !strings.Contains(stdout, "120") {
		t.Errorf("« lxc show » doit proposer les conteneurs : %q", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "211\t") {
			t.Errorf("une VM est proposée par « lxc show » : %q", stdout)
		}
	}
}

// The cache is what keeps hammering Tab from hammering the API.
func TestCompletionCachesTheInventory(t *testing.T) {
	isolateCompletionCache(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/resources": "cluster-resources-full.json",
	})
	point(t, srv.URL)

	for range 3 {
		completeArgs(t, "vm", "show", "")
	}

	if len(srv.Requests) != 1 {
		t.Errorf("%d appels à /cluster/resources pour trois Tab — le cache ne sert pas : %v",
			len(srv.Requests), srv.Requests)
	}
}

// The cache is keyed by endpoint AND identity: two contexts must not see each
// other's inventory, and a narrowed token must not keep offering what it can no
// longer read.
func TestCompletionCacheIsKeyedByEndpointAndIdentity(t *testing.T) {
	a := completionCachePath("https://pve1:8006", "automation@pve!pvectl")
	b := completionCachePath("https://pve2:8006", "automation@pve!pvectl")
	c := completionCachePath("https://pve1:8006", "autre@pve!jetable")

	if a == b || a == c || b == c {
		t.Errorf("les chemins de cache se confondent :\n  %s\n  %s\n  %s", a, b, c)
	}
	// A token id in a filename would leak an identity to anything listing the
	// cache directory.
	if strings.Contains(a, "automation") || strings.Contains(a, "pvecli!") {
		t.Errorf("le chemin de cache expose l'identité : %s", a)
	}
}

func TestCompletionCacheIsWrittenPrivate(t *testing.T) {
	isolateCompletionCache(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/resources": "cluster-resources-full.json",
	})
	point(t, srv.URL)

	completeArgs(t, "vm", "show", "")

	path := completionCachePath(srv.URL, "automation@pve!pvectl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("le cache n'a pas été écrit : %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode du cache = %o, want 600 — il décrit une infrastructure", perm)
	}
}

// The completion command must explain where its output goes: a script nobody
// knows how to install is a feature nobody uses.
func TestCompletionCommandDocumentsItsInstallation(t *testing.T) {
	stdout, _, err := run(t, "completion", "--help")
	if err != nil {
		t.Fatalf("completion --help: %v", err)
	}
	for _, want := range []string{"fpath", "bash_completion.d", "fish", "cluster/resources"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("l'aide ne mentionne pas %q", want)
		}
	}
}

// Every command taking a <vmid>, a <storage> or a <poolid> must be served,
// including the ones added after this was written. Walking the tree is what
// makes that true without a list to maintain.
func TestEveryTypedArgumentIsCompleted(t *testing.T) {
	root := NewRootCmd("dev", "abc1234")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if !c.Runnable() || c.ValidArgsFunction != nil {
			return
		}
		for _, token := range []string{"<vmid>", "<storage>", "<poolid>"} {
			if strings.Contains(c.Use, token) {
				t.Errorf("« %s » prend un %s et ne le complète pas", c.CommandPath(), token)
			}
		}
	}
	walk(root)
}
