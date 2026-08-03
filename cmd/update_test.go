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

// 1. A "dev" binary never touches the network, in either mode.
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

// 6. A network outage must stay silent in --notify, exit 0, and still stamp
// the cache — that stamp is what protects the anti-hammering guarantee of
// test 5: without it, an offline machine would retry on every new terminal.
func TestUpdateCheckNetworkFailureStaysSilentAndStampsCache(t *testing.T) {
	isolateCompletionCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withGithubAPI(t, srv.URL)

	before := time.Now()
	stdout, _, err := runUpdate(t, "v0.1.0", "--notify")
	if err != nil {
		t.Fatalf("--notify must exit 0 on a network failure, got: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on a network failure", stdout)
	}

	c, readErr := readUpdateCache(updateCachePath())
	if readErr != nil {
		t.Fatalf("cache was not written after a failed check: %v", readErr)
	}
	if c.LatestTag != "" {
		t.Errorf("latest_tag = %q, want empty after a failed fetch", c.LatestTag)
	}
	if c.CheckedAt.Before(before) {
		t.Errorf("checked_at = %v, want it stamped at (or after) %v", c.CheckedAt, before)
	}
}

// 7. The cache file itself must be 0600, same rule as the completion cache.
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

// 8. --force ignores a fresh cache and forces a new request.
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
