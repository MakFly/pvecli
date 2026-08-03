package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

// updateCheckSuccessTTL bounds how often a shell is willing to pay for a call
// to the GitHub API after a SUCCESSFUL check. 24h, not 10s like the
// completion cache (PVX-053): this answers a question the operator asks once
// a day at most, and GitHub's unauthenticated rate limit does not forgive a
// CLI that asks it once per terminal.
const updateCheckSuccessTTL = 24 * time.Hour

// updateCheckFailureTTL bounds how long a FAILED check is remembered.
//
// It is deliberately much shorter than updateCheckSuccessTTL. GitHub's
// anonymous quota (60 req/h) is counted per IP, not per machine: behind a
// shared NAT — an office, a VPN, a CI runner — it can be exhausted by
// something this binary never did. If a failure were cached for 24h like a
// success, one transient 403 would silence --notify for a full day even
// after the quota resets, and nothing on the prompt would explain why. 1h is
// long enough to still avoid hammering GitHub every terminal, short enough
// that the feature heals itself once the underlying cause (offline, rate
// limit, GitHub outage) is gone.
const updateCheckFailureTTL = 1 * time.Hour

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

// updateCacheSchema tags the shape of updateCheckCache below. Bump it
// whenever a field is added or its meaning changes, and treat any cache
// whose Schema does not match as absent (see readUpdateCache) — never as a
// silent, empty success. Without this, a cache written by an older binary
// (which never wrote a Success bit) would unmarshal Success as its Go zero
// value, false, and be indistinguishable from a genuine failure: exactly the
// kind of coincidence that must not be load-bearing.
const updateCacheSchema = 2

// updateCheckCache is what gets written to disk.
//
// Success says explicitly whether the last attempt reached GitHub, rather
// than leaving the reader to infer it from LatestTag being empty. The two
// used to be the same fact by coincidence; making it a named field means the
// next person touching this code does not have to rediscover that coupling
// — and it is what lets readFreshUpdateCache pick the right TTL below.
type updateCheckCache struct {
	Schema    int       `json:"schema"`
	CheckedAt time.Time `json:"checked_at"`
	LatestTag string    `json:"latest_tag"`
	Success   bool      `json:"success"`
}

func newUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update",
		Short: "Vérifie les mises à jour de pvecli lui-même",
		Long: `Regroupe les commandes qui parlent de la version du binaire pvecli, pas de
celle du nœud PVE (voir « pvecli version » pour cette dernière).`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newUpdateCheckCmd(), newUpdateInstallHookCmd())
	return c
}

func newUpdateCheckCmd() *cobra.Command {
	var notify, refresh, force bool

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

La réponse est mise en cache 24h. Sans drapeau, cette commande peut lire ET
écrire ce cache (elle est appelée par un humain qui attend une réponse).

  --notify    LECTURE SEULE — n'ouvre jamais de connexion réseau, même si le
              cache est absent ou périmé. Silencieux sauf si une mise à jour
              est déjà connue ; toujours code 0. C'est le mode que le snippet
              zsh imprime au premier plan, avant le prompt.
  --refresh   ÉCRITURE SEULE — ne parle jamais à l'humain (aucune sortie,
              jamais, y compris en erreur) ; ne fait que rafraîchir le cache
              si son TTL est dépassé. C'est le mode que le snippet zsh lance
              en arrière-plan, pour la PROCHAINE ouverture de terminal.
  --force     ignore le TTL du cache et refait l'appel réseau. Incompatible
              avec --notify, qui ne va jamais au réseau ; combine-le avec
              --refresh pour forcer un rafraîchissement à la main.

--notify et --refresh sont volontairement deux commandes différentes, jamais
une seule : une commande qui doit à la fois répondre instantanément à un
humain ET pouvoir attendre 2s sur le réseau ne peut pas tenir les deux
promesses en même temps sans imprimer sa réponse de façon asynchrone — et une
ligne qui s'affiche after-coup au milieu d'une saisie en cours est pire que
ce que ce découpage évite.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if notify && refresh {
				return &exitError{code: pve.ExitUsage,
					msg: "--notify et --refresh sont incompatibles : --notify ne lit que le cache, --refresh ne parle qu'au réseau"}
			}
			if notify && force {
				return &exitError{code: pve.ExitUsage,
					msg: "--force n'a pas de sens avec --notify (qui ne va jamais au réseau) : utilise --refresh --force"}
			}
			return runUpdateCheck(cmd, notify, refresh, force)
		},
	}

	c.Flags().BoolVar(&notify, "notify", false,
		"lecture seule du cache, jamais de réseau (pour un .zshrc, au premier plan)")
	c.Flags().BoolVar(&refresh, "refresh", false,
		"rafraîchit le cache si périmé, aucune sortie jamais (pour un .zshrc, en arrière-plan)")
	c.Flags().BoolVar(&force, "force", false, "ignore le TTL du cache et refait l'appel réseau")
	return c
}

func runUpdateCheck(cmd *cobra.Command, notify, refresh, force bool) error {
	installed := cmd.Root().Version
	out := cmd.OutOrStdout()

	// A binary built locally is not a release, and comparing it to one always
	// loses: this is the exact rule install.sh already applies for
	// PVECLI_ONLY_IF_NEWER, reproduced here so the two never disagree. No
	// network call, no cache write, no output in any mode — there is nothing
	// here worth remembering or reporting.
	if installed == "dev" {
		if !notify && !refresh {
			_, _ = fmt.Fprintln(out, "pvecli compilé localement (dev) — vérification désactivée")
		}
		return nil
	}

	// --refresh only ever touches the cache, never the human. Its output is
	// always empty, even on a network failure: it is meant to run detached in
	// the background, and a background job that writes to a terminal the user
	// is typing into is the exact bug this split exists to close.
	if refresh {
		_, _ = resolveLatestTag(cmd.Context(), force)
		return nil
	}

	// --notify never opens a socket: it is the branch a new shell prompt
	// waits on, so it may only ever be as slow as reading one file. This
	// means it can lag one terminal behind the truth — it shows whatever
	// --refresh last recorded, not whatever GitHub says right now. That lag
	// is the deliberate price of never blocking a prompt; --refresh (run in
	// background by the same snippet) is what keeps the lag at "one tick",
	// not "forever".
	if notify {
		// Choix assumé : --notify ne signale jamais « je n'ai pas pu vérifier
		// depuis N jours », même quand le cache est en échec depuis longtemps.
		// Un shell qui râle à chaque ouverture parce que GitHub est
		// injoignable serait une nuisance pire que le silence qu'il est censé
		// garder — et « pvecli update check » en mode humain donne déjà la
		// raison exacte à qui la demande. Si ce choix doit changer un jour,
		// c'est ici qu'il faudrait ajouter la branche, pas le deviner.
		c, err := readFreshUpdateCache()
		if err != nil || c.LatestTag == "" || c.LatestTag == installed {
			return nil
		}
		_, _ = fmt.Fprintf(out, "%s → %s disponible : https://github.com/%s/releases/latest\n", installed, c.LatestTag, updateRepo)
		return nil
	}

	// Plain human mode: free to reach the network, exactly as before.
	tag, fetchErr := resolveLatestTag(cmd.Context(), force)

	if tag == "" {
		reason := "cause inconnue"
		if fetchErr != nil {
			reason = fetchErr.Error()
		}
		_, _ = fmt.Fprintf(out, "vérification impossible : %s\n", reason)
		return nil
	}

	if tag == installed {
		_, _ = fmt.Fprintf(out, "pvecli est à jour (%s)\n", installed)
		return nil
	}

	_, _ = fmt.Fprintf(out, "%s → %s disponible : https://github.com/%s/releases/latest\n", installed, tag, updateRepo)
	return nil
}

// readFreshUpdateCache reads the cache and rejects it if its TTL has
// expired. It never touches the network — this is the one function --notify
// is allowed to call.
//
// The TTL applied depends on what the cache actually recorded: a success
// gets updateCheckSuccessTTL (24h), a failure gets the much shorter
// updateCheckFailureTTL (1h) — see its doc comment for why a single shared
// TTL was the bug. A cache in an obsolete schema is rejected outright by
// readUpdateCache below, so it is never mistaken for either.
func readFreshUpdateCache() (*updateCheckCache, error) {
	c, err := readUpdateCache(updateCachePath())
	if err != nil {
		return nil, err
	}
	ttl := updateCheckFailureTTL
	if c.Success {
		ttl = updateCheckSuccessTTL
	}
	if time.Since(c.CheckedAt) >= ttl {
		return nil, fmt.Errorf("cache périmé")
	}
	return c, nil
}

// resolveLatestTag serves the cache when it is fresh for its own TTL (see
// readFreshUpdateCache), and reaches GitHub otherwise. It never returns an
// error to a caller that would turn it into a non-zero exit: fetchErr is
// informational, meant only for the human-readable message in the
// non-notify path.
//
// Point critique : un échec réseau est un état, pas une absence. Le cache est
// réécrit avec un tag vide et un checked_at frais MÊME QUAND l'appel échoue.
// Sans cela, un poste hors ligne ne verrait jamais le cache devenir « frais »
// et relancerait l'appel réseau — donc payerait le timeout de 2s — à CHAQUE
// ouverture de terminal, ce qui est exactement le martèlement que ce cache
// existe pour éviter. Ce qui a changé : cet horodatage-en-échec n'est plus
// couvert par le même TTL qu'un succès (voir updateCheckFailureTTL) — un
// échec transitoire (quota GitHub épuisé par une IP partagée, panne courte)
// ne doit plus rendre --notify muet pour 24h.
func resolveLatestTag(ctx context.Context, force bool) (tag string, fetchErr error) {
	if !force {
		if c, err := readFreshUpdateCache(); err == nil {
			return c.LatestTag, nil
		}
	}

	tag, fetchErr = fetchLatestTag(ctx)
	writeUpdateCache(updateCachePath(), &updateCheckCache{
		Schema:    updateCacheSchema,
		CheckedAt: time.Now(),
		LatestTag: tag,
		Success:   fetchErr == nil,
	})
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
	defer func() { _ = resp.Body.Close() }()

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
	// A cache written by an older schema (notably: no Success field at all)
	// must be treated as absent, never as a silent success — see
	// updateCacheSchema's doc comment for why that coincidence would be a bug.
	if c.Schema != updateCacheSchema {
		return nil, fmt.Errorf("cache dans un format obsolète")
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
