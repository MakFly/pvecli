package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// runUpdate is like run() (cmd/root_test.go) but lets a test choose the
// binary's own version — run() hardcodes "dev", which is exactly the one
// value most of these tests need to NOT be.
func runUpdate(t *testing.T, version string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCmd(version, "abc1234")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"update", "check"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// withGithubAPI points githubAPIBase at a test server for the lifetime of the
// test, and restores it afterwards so other tests keep hitting the real
// (never actually dialed) default.
func withGithubAPI(t *testing.T, url string) {
	t.Helper()
	orig := githubAPIBase
	githubAPIBase = url
	t.Cleanup(func() { githubAPIBase = orig })
}

// failIfCalled is a server that fails the test the moment a request reaches
// it — the only reliable way to prove "no network call was made", since a
// missing assertion proves nothing.
func failIfCalled(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("update check must not reach the network here")
	}))
}

func releaseServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
}

func writeCacheFile(t *testing.T, checkedAt time.Time, tag string) {
	t.Helper()
	path := updateCachePath()
	writeUpdateCache(path, &updateCheckCache{CheckedAt: checkedAt, LatestTag: tag})
}

// 1. A "dev" binary never touches the network, in any mode.
func TestUpdateCheckDevVersionNeverCallsNetwork(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)

	stdout, _, err := run(t, "update", "check", "--notify")
	if err != nil {
		t.Fatalf("--notify returned an error: %v", err)
	}
	if stdout != "" {
		t.Errorf("--notify stdout = %q, want empty for a dev binary", stdout)
	}

	stdout, _, err = run(t, "update", "check", "--refresh")
	if err != nil {
		t.Fatalf("--refresh returned an error: %v", err)
	}
	if stdout != "" {
		t.Errorf("--refresh stdout = %q, want empty for a dev binary", stdout)
	}

	stdout, _, err = run(t, "update", "check")
	if err != nil {
		t.Fatalf("human mode returned an error: %v", err)
	}
	if !strings.Contains(stdout, "dev") {
		t.Errorf("human mode stdout = %q, want it to mention the dev build", stdout)
	}
}

// 2. A fresh cache is served without any network call.
func TestUpdateCheckFreshCacheMakesNoRequest(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now(), "v0.1.0")

	stdout, _, err := runUpdate(t, "v0.1.0")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if !strings.Contains(stdout, "v0.1.0") {
		t.Errorf("stdout = %q, want it to mention v0.1.0 (served from cache)", stdout)
	}
}

// 3. A stale cache triggers exactly one request and gets rewritten.
func TestUpdateCheckStaleCacheRefetches(t *testing.T) {
	isolateCompletionCache(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0"}`))
	}))
	defer srv.Close()
	withGithubAPI(t, srv.URL)
	writeCacheFile(t, time.Now().Add(-25*time.Hour), "v0.1.0")

	stdout, _, err := runUpdate(t, "v0.1.0")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1", calls)
	}
	if !strings.Contains(stdout, "v0.2.0") {
		t.Errorf("stdout = %q, want the freshly fetched v0.2.0", stdout)
	}

	c, err := readUpdateCache(updateCachePath())
	if err != nil {
		t.Fatalf("cache was not rewritten: %v", err)
	}
	if c.LatestTag != "v0.2.0" {
		t.Errorf("cached latest_tag = %q, want v0.2.0", c.LatestTag)
	}
}

// 4. --notify with the installed version equal to the latest release: strictly empty.
func TestUpdateCheckNotifySilentWhenUpToDate(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now(), "v0.1.0")

	stdout, _, err := runUpdate(t, "v0.1.0", "--notify")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want strictly empty when up to date", stdout)
	}
}

// 5. --notify with a different release: exactly one line, both versions in it.
func TestUpdateCheckNotifyPrintsOneLineWhenOutdated(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now(), "v0.2.0")

	stdout, _, err := runUpdate(t, "v0.1.0", "--notify")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stdout = %q, want exactly one line", stdout)
	}
	if !strings.Contains(lines[0], "v0.1.0") || !strings.Contains(lines[0], "v0.2.0") {
		t.Errorf("line = %q, want both v0.1.0 and v0.2.0 mentioned", lines[0])
	}
}

// 6. A network outage must stay silent under --refresh, exit 0, and still
// stamp the cache — that stamp is what protects --notify's anti-hammering
// guarantee: without it, an offline machine's background refresh would
// retry the same 2s-timeout request on every single terminal, which is the
// exact hammering this split exists to prevent. --refresh is the ONLY mode
// allowed to reach the network without a human waiting on it, so it is also
// the only mode this failure scenario applies to now — tests 9/10 below pin
// what --notify itself must do in the same situation (nothing, ever).
func TestUpdateCheckRefreshNetworkFailureStaysSilentAndStampsCache(t *testing.T) {
	isolateCompletionCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withGithubAPI(t, srv.URL)

	before := time.Now()
	stdout, stderr, err := runUpdate(t, "v0.1.0", "--refresh")
	if err != nil {
		t.Fatalf("--refresh must exit 0 on a network failure, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout=%q stderr=%q, want both strictly empty on a network failure", stdout, stderr)
	}

	c, readErr := readUpdateCache(updateCachePath())
	if readErr != nil {
		t.Fatalf("cache was not written after a failed refresh: %v", readErr)
	}
	if c.LatestTag != "" {
		t.Errorf("latest_tag = %q, want empty after a failed fetch", c.LatestTag)
	}
	if c.CheckedAt.Before(before) {
		t.Errorf("checked_at = %v, want it stamped at (or after) %v", c.CheckedAt, before)
	}
}

// 9. --notify with NO cache at all must still make zero HTTP calls. The
// previous version of this file never checked this: the old --notify fell
// back to the network on a cache miss, which meant the zsh snippet's
// foreground call could block on a socket — exactly what --notify exists to
// never do.
func TestUpdateCheckNotifyNeverTouchesNetworkWithNoCache(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)

	stdout, _, err := runUpdate(t, "v0.1.0", "--notify")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty when there is no cache yet", stdout)
	}
}

// 10. --notify with a STALE cache must also make zero HTTP calls — staleness
// is precisely the case the old implementation used to refresh from the
// network.
func TestUpdateCheckNotifyNeverTouchesNetworkWithStaleCache(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now().Add(-25*time.Hour), "v0.2.0")

	stdout, _, err := runUpdate(t, "v0.1.0", "--notify")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty with a stale cache (nothing may refresh it here)", stdout)
	}
}

// 11. --refresh talks to the network but never to the human: strictly empty
// on stdout AND stderr regardless of outcome, and the cache gets rewritten.
func TestUpdateCheckRefreshIsSilentAndRewritesCache(t *testing.T) {
	isolateCompletionCache(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0"}`))
	}))
	defer srv.Close()
	withGithubAPI(t, srv.URL)
	writeCacheFile(t, time.Now().Add(-25*time.Hour), "v0.1.0")

	stdout, stderr, err := runUpdate(t, "v0.1.0", "--refresh")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout=%q stderr=%q, want both strictly empty", stdout, stderr)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1", calls)
	}

	c, err := readUpdateCache(updateCachePath())
	if err != nil {
		t.Fatalf("cache was not rewritten: %v", err)
	}
	if c.LatestTag != "v0.2.0" {
		t.Errorf("cached latest_tag = %q, want v0.2.0", c.LatestTag)
	}
}

// 12. The exact sequence the zsh snippet relies on: --refresh populates the
// cache once, and a LATER --notify (as in the next terminal) reads it back
// without any network access of its own.
func TestUpdateCheckRefreshThenNotifySequence(t *testing.T) {
	isolateCompletionCache(t)
	srv := releaseServer(t, "v0.2.0")
	defer srv.Close()
	withGithubAPI(t, srv.URL)

	if _, _, err := runUpdate(t, "v0.1.0", "--refresh"); err != nil {
		t.Fatalf("--refresh returned an error: %v", err)
	}

	// The second call must not need the network at all: swap in a server
	// that fails the test if it is ever dialed, then rely purely on the
	// cache --refresh just wrote.
	withGithubAPI(t, failIfCalled(t).URL)
	stdout, _, err := runUpdate(t, "v0.1.0", "--notify")
	if err != nil {
		t.Fatalf("--notify returned an error: %v", err)
	}
	if !strings.Contains(stdout, "v0.1.0") || !strings.Contains(stdout, "v0.2.0") {
		t.Errorf("stdout = %q, want it to report v0.1.0 -> v0.2.0 from the cache --refresh just wrote", stdout)
	}
}

// 13. --notify and --refresh contradict each other (read-only vs write-only)
// and must be rejected as a usage error, not silently reconciled.
func TestUpdateCheckNotifyAndRefreshAreIncompatible(t *testing.T) {
	isolateCompletionCache(t)
	if _, _, err := runUpdate(t, "v0.1.0", "--notify", "--refresh"); err == nil {
		t.Fatal("want a usage error when combining --notify and --refresh")
	}
}

// 14. --force means "go to the network", which --notify by contract never
// does — combining them must fail loudly rather than pretend to honor both.
func TestUpdateCheckNotifyAndForceAreIncompatible(t *testing.T) {
	isolateCompletionCache(t)
	if _, _, err := runUpdate(t, "v0.1.0", "--notify", "--force"); err == nil {
		t.Fatal("want a usage error when combining --notify and --force")
	}
}

// 15. The cache file itself must be 0600, same rule as the completion cache.
func TestUpdateCheckCacheFilePermissions(t *testing.T) {
	isolateCompletionCache(t)
	srv := releaseServer(t, "v0.1.0")
	defer srv.Close()
	withGithubAPI(t, srv.URL)

	if _, _, err := runUpdate(t, "v0.1.0"); err != nil {
		t.Fatalf("returned an error: %v", err)
	}

	info, err := os.Stat(updateCachePath())
	if err != nil {
		t.Fatalf("stat cache file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file mode = %o, want 0600", perm)
	}
}

// 16. --force ignores a fresh cache and forces a new request.
func TestUpdateCheckForceIgnoresFreshCache(t *testing.T) {
	isolateCompletionCache(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0"}`))
	}))
	defer srv.Close()
	withGithubAPI(t, srv.URL)
	writeCacheFile(t, time.Now(), "v0.1.0")

	stdout, _, err := runUpdate(t, "v0.1.0", "--force")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 despite a fresh cache", calls)
	}
	if !strings.Contains(stdout, "v0.3.0") {
		t.Errorf("stdout = %q, want the forced fetch's v0.3.0", stdout)
	}
}
