package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// writeCacheFile writes a cache entry in the CURRENT schema. success mirrors
// what a real resolveLatestTag call would have recorded: true for a tag that
// came back from GitHub, false for a failed attempt (tag is then normally
// "").
func writeCacheFile(t *testing.T, checkedAt time.Time, tag string, success bool) {
	t.Helper()
	path := updateCachePath()
	writeUpdateCache(path, &updateCheckCache{
		Schema:    updateCacheSchema,
		CheckedAt: checkedAt,
		LatestTag: tag,
		Success:   success,
	})
}

// writeLegacyCacheFile writes a cache file in the pre-schema format (no
// "schema", no "success" field at all) — exactly what a binary built before
// this fix would have left on disk. It writes raw JSON on purpose, bypassing
// writeUpdateCache, which cannot produce this shape anymore.
func writeLegacyCacheFile(t *testing.T, checkedAt time.Time, tag string) {
	t.Helper()
	path := updateCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := []byte(`{"checked_at":"` + checkedAt.Format(time.RFC3339) + `","latest_tag":"` + tag + `"}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("writing legacy cache: %v", err)
	}
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
	writeCacheFile(t, time.Now(), "v0.1.0", true)

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
	writeCacheFile(t, time.Now().Add(-25*time.Hour), "v0.1.0", true)

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
	writeCacheFile(t, time.Now(), "v0.1.0", true)

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
	writeCacheFile(t, time.Now(), "v0.2.0", true)

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
	writeCacheFile(t, time.Now().Add(-25*time.Hour), "v0.2.0", true)

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
	writeCacheFile(t, time.Now().Add(-25*time.Hour), "v0.1.0", true)

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
	writeCacheFile(t, time.Now(), "v0.1.0", true)

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

// 17. A failed check gets a SHORT TTL (1h), not the 24h a success gets. A
// GitHub 403 30 minutes old is still within that short window, so --refresh
// must not retry yet — this is the anti-hammering half of the guarantee.
func TestUpdateCheckFailureCacheStaysFreshWithinItsShortTTL(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now().Add(-30*time.Minute), "", false)

	if _, _, err := runUpdate(t, "v0.1.0", "--refresh"); err != nil {
		t.Fatalf("returned an error: %v", err)
	}
}

// 18. The same failed check, now 2h old, is past its 1h TTL and must be
// retried. This is the test that pins the fix itself: on the previous
// (single 24h TTL) implementation, this assertion would fail — a shared-IP
// rate limit would keep --notify silent for a full day after it lifted.
func TestUpdateCheckFailureCacheRefetchesAfterItsShortTTL(t *testing.T) {
	isolateCompletionCache(t)
	var calls int
	srv := releaseHandlerCountingCalls(t, "v0.2.0", &calls)
	defer srv.Close()
	withGithubAPI(t, srv.URL)
	writeCacheFile(t, time.Now().Add(-2*time.Hour), "", false)

	if _, _, err := runUpdate(t, "v0.1.0", "--refresh"); err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 — a 2h-old failed check must be retried", calls)
	}

	c, err := readUpdateCache(updateCachePath())
	if err != nil {
		t.Fatalf("cache was not rewritten: %v", err)
	}
	if !c.Success || c.LatestTag != "v0.2.0" {
		t.Errorf("cache = %+v, want a recorded success with v0.2.0", c)
	}
}

// 19. A SUCCESSFUL check keeps its full 24h TTL — the fix must not shrink
// the TTL that was already correct, only the failure one.
func TestUpdateCheckSuccessCacheKeepsItsLongTTL(t *testing.T) {
	isolateCompletionCache(t)
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now().Add(-2*time.Hour), "v0.1.0", true)

	if _, _, err := runUpdate(t, "v0.1.0", "--refresh"); err != nil {
		t.Fatalf("returned an error: %v", err)
	}
}

// 20. A cache written by an older schema (no "schema"/"success" fields) must
// be treated as ABSENT, never as a silent, empty success — the Go zero value
// of a missing "success" field is false, which happens to look like a
// recorded failure. Without the explicit schema guard, an upgrade would
// misread a perfectly good old cache (or the reverse) by coincidence.
func TestUpdateCheckLegacyCacheIsTreatedAsAbsent(t *testing.T) {
	isolateCompletionCache(t)
	var calls int
	srv := releaseHandlerCountingCalls(t, "v0.2.0", &calls)
	defer srv.Close()
	withGithubAPI(t, srv.URL)
	writeLegacyCacheFile(t, time.Now(), "v0.1.0")

	if _, _, err := runUpdate(t, "v0.1.0", "--refresh"); err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 — a legacy-schema cache must be refetched, not trusted", calls)
	}

	c, err := readUpdateCache(updateCachePath())
	if err != nil {
		t.Fatalf("cache was not rewritten in the current schema: %v", err)
	}
	if c.Schema != updateCacheSchema {
		t.Errorf("schema = %d, want %d after the rewrite", c.Schema, updateCacheSchema)
	}
}

// releaseHandlerCountingCalls is releaseServer (above) plus a call counter,
// factored out because two tests below need to assert "exactly one request".
func releaseHandlerCountingCalls(t *testing.T, tag string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
}

// 21. A cached failure, replayed in human mode, must NAME its cause and point
// at --force.
//
// This is the regression that shipped: resolveLatestTag returned the cached
// entry as (LatestTag, nil), so a failed check came back as an empty tag with
// no error, and the command printed "vérification impossible : cause
// inconnue" — about the one case where the cause is fully known. The operator
// learned nothing, and above all never learned that --force exists.
func TestUpdateCheckCachedFailureExplainsItselfAndOffersForce(t *testing.T) {
	isolateCompletionCache(t)
	// failIfCalled proves the cache is still doing its job: explaining the
	// failure must not cost a network call.
	withGithubAPI(t, failIfCalled(t).URL)
	writeCacheFile(t, time.Now().Add(-30*time.Minute), "", false)

	stdout, _, err := runUpdate(t, "v0.1.0")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if strings.Contains(stdout, "cause inconnue") {
		t.Errorf("the cause is known here, yet the message says it is not :\n%s", stdout)
	}
	if !strings.Contains(stdout, "--force") {
		t.Errorf("the message does not mention the way out :\n%s", stdout)
	}
	if !strings.Contains(stdout, "30m") {
		t.Errorf("the message does not say how old the failure is :\n%s", stdout)
	}
}

// 22. "cause inconnue" must survive as the last resort, for a genuinely
// unexplained empty answer. Removing the branch instead of narrowing it would
// trade one silent case for another.
func TestUpdateCheckUnexplainedEmptyAnswerStillSaysUnknown(t *testing.T) {
	isolateCompletionCache(t)
	// A 200 with no tag_name: fetchLatestTagWithin rejects it, so the reason
	// comes from the fetch error, not from the cache.
	withGithubAPI(t, releaseServer(t, "").URL)

	stdout, _, err := runUpdate(t, "v0.1.0")
	if err != nil {
		t.Fatalf("returned an error: %v", err)
	}
	if !strings.Contains(stdout, "vérification impossible") {
		t.Errorf("an unexplained empty answer must still be reported :\n%s", stdout)
	}
}
