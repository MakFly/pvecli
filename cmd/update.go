package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// updateCheckTTL bounds how often a shell is willing to pay for a call to the
// GitHub API. 24h, not 10s like the completion cache (PVX-053): this answers
// a question the operator asks once a day at most, and GitHub's unauthenticated
// rate limit does not forgive a CLI that asks it once per terminal.
const updateCheckTTL = 24 * time.Hour

// updateCheckTimeout caps the one HTTP call this command may make. A shell
// must never wait on it: 2s is the same budget the completion path uses for
// the same reason (cmd/completion.go).
const updateCheckTimeout = 2 * time.Second

// updateRepo is the GitHub repository whose releases this command watches.
const updateRepo = "MakFly/pvecli"

// githubAPIBase is a variable, not a constant, so a test can point it at an
// httptest.Server instead of the real GitHub API. Nothing in this package
// mutates it outside tests.
var githubAPIBase = "https://api.github.com"

// updateCheckCache is what gets written to disk. LatestTag empty means the
// last attempt to reach GitHub failed — see resolveLatestTag.
type updateCheckCache struct {
	CheckedAt time.Time `json:"checked_at"`
	LatestTag string    `json:"latest_tag"`
}

func newUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update",
		Short: "Vérifie les mises à jour de pvecli lui-même",
		Long: `Regroupe les commandes qui parlent de la version du binaire pvecli, pas de
celle du nœud PVE (voir « pvecli version » pour cette dernière).`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newUpdateCheckCmd())
	return c
}

func newUpdateCheckCmd() *cobra.Command {
	var notify, force bool

	c := &cobra.Command{
		Use:   "check",
		Short: "Compare ce binaire à la dernière release publiée sur GitHub",
		Long: `Interroge GET /repos/` + updateRepo + `/releases/latest et compare le tag
obtenu à la version de ce binaire (pvecli --version).

La comparaison est une égalité de chaînes, pas un ordre sémantique : on
détecte « autre chose que ce qui tourne », pas « plus récent » — la même règle
que install.sh (PVECLI_ONLY_IF_NEWER), pour ne jamais se contredire avec lui.

Un binaire compilé localement (« dev ») contient presque toujours plus que la
dernière release publiée ; le signaler comme périmé serait un faux positif
quotidien, donc cette commande s'abstient totalement pour lui — y compris de
tout appel réseau.

La réponse est mise en cache 24h. C'est ce qui permet à --notify d'être
appelée à chaque ouverture de terminal sans marteler l'API GitHub.

  --notify   silencieux sauf si une mise à jour existe ; toujours code 0,
             même hors ligne. C'est le mode que le snippet zsh appelle.
  --force    ignore le cache et refait l'appel réseau, pour tester à la main.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdateCheck(cmd, notify, force)
		},
	}

	c.Flags().BoolVar(&notify, "notify", false,
		"silencieux sauf si une mise à jour existe (pour un .zshrc) ; toujours code 0")
	c.Flags().BoolVar(&force, "force", false, "ignore le TTL du cache et refait l'appel réseau")
	return c
}

func runUpdateCheck(cmd *cobra.Command, notify, force bool) error {
	installed := cmd.Root().Version
	out := cmd.OutOrStdout()

	// A binary built locally is not a release, and comparing it to one always
	// loses: this is the exact rule install.sh already applies for
	// PVECLI_ONLY_IF_NEWER, reproduced here so the two never disagree. No
	// network call, no cache write — there is nothing here worth remembering.
	if installed == "dev" {
		if !notify {
			fmt.Fprintln(out, "pvecli compilé localement (dev) — vérification désactivée")
		}
		return nil
	}

	tag, fetchErr := resolveLatestTag(cmd.Context(), force)

	if tag == "" {
		if !notify {
			reason := "cause inconnue"
			if fetchErr != nil {
				reason = fetchErr.Error()
			}
			fmt.Fprintf(out, "vérification impossible : %s\n", reason)
		}
		return nil
	}

	if tag == installed {
		if !notify {
			fmt.Fprintf(out, "pvecli est à jour (%s)\n", installed)
		}
		return nil
	}

	fmt.Fprintf(out, "%s → %s disponible : https://github.com/%s/releases/latest\n", installed, tag, updateRepo)
	return nil
}

// resolveLatestTag serves the 24h cache when it is fresh, and reaches GitHub
// otherwise. It never returns an error to a caller that would turn it into a
// non-zero exit: fetchErr is informational, meant only for the human-readable
// message in the non-notify path.
//
// Point critique : un échec réseau est un état, pas une absence. Le cache est
// réécrit avec un tag vide et un checked_at frais MÊME QUAND l'appel échoue.
// Sans cela, un poste hors ligne ne verrait jamais le cache devenir « frais »
// et relancerait l'appel réseau — donc payerait le timeout de 2s — à CHAQUE
// ouverture de terminal, ce qui est exactement le martèlement que ce cache
// existe pour éviter.
func resolveLatestTag(ctx context.Context, force bool) (tag string, fetchErr error) {
	path := updateCachePath()

	if !force {
		if c, err := readUpdateCache(path); err == nil && time.Since(c.CheckedAt) < updateCheckTTL {
			return c.LatestTag, nil
		}
	}

	tag, fetchErr = fetchLatestTag(ctx)
	writeUpdateCache(path, &updateCheckCache{CheckedAt: time.Now(), LatestTag: tag})
	return tag, fetchErr
}

// fetchLatestTag makes the single HTTP call this command is allowed to make.
func fetchLatestTag(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	url := githubAPIBase + "/repos/" + updateRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: updateCheckTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub a répondu %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", fmt.Errorf("réponse GitHub sans tag_name")
	}
	return body.TagName, nil
}

// updateCachePath mirrors completionCachePath (cmd/completion.go): same
// os.UserCacheDir() root, same "pvecli" subdirectory. It is not keyed by
// endpoint or identity — the answer does not depend on which cluster is
// configured, only on which binary is running.
func updateCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "pvecli", "update-check.json")
}

func readUpdateCache(path string) (*updateCheckCache, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c updateCheckCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// writeUpdateCache is best-effort, like writeCompletionCache: a cache that
// cannot be written means the next terminal pays for a fresh check, not that
// this one should fail.
func writeUpdateCache(path string, c *updateCheckCache) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}
