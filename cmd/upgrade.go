package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

// upgradeTagTimeout is the budget for asking GitHub which release is the
// latest. Ten times updateCheckTimeout (cmd/update.go), and deliberately so:
// that 2s exists because a shell prompt is waiting on it, and nothing must
// ever make a terminal feel slow. Here a human has typed `upgrade` and is
// watching — the failure to avoid is not "slow", it is "gave up on a
// mediocre link and told me GitHub was down".
const upgradeTagTimeout = 10 * time.Second

// upgradeDownloadTimeout covers the two downloads (the binary and its sums).
// The binary is ~15 MB; a minute is generous on a bad connection and still
// bounded, which is what matters — an upgrade that hangs forever is worse
// than one that fails and can be retried.
const upgradeDownloadTimeout = 60 * time.Second

// githubDownloadBase is where release ASSETS live. Distinct from
// githubAPIBase (cmd/update.go) because GitHub serves them from a different
// host, and a variable rather than a constant for the same reason: a test
// must be able to point it at an httptest.Server.
var githubDownloadBase = "https://github.com"

// upgradeSums is the name of the checksum file published alongside the
// binaries. It comes from `make release` (shasum -a 256 * > SHA256SUMS) and
// is re-verified by the release workflow before publication — this command
// is the third and last party to check it, on the machine that will actually
// run the bytes.
const upgradeSums = "SHA256SUMS"

func newUpgradeCmd() *cobra.Command {
	var dryRun, force bool

	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Remplace ce binaire par la dernière release publiée",
		Long: `Télécharge la dernière release de ` + updateRepo + ` pour cette plateforme,
VÉRIFIE SA SOMME SHA-256, et ne remplace le binaire courant qu'ensuite.

C'est le pendant exécutable de « pvecli update check », qui se contente de
signaler. Le contrôle appliqué est exactement celui d'install.sh : la somme
publiée est confrontée aux octets reçus AVANT que quoi que ce soit ne touche
au disque, et une somme qui ne correspond pas interrompt tout sans rien
écrire.

Le remplacement est atomique : le binaire est écrit à côté de sa destination,
dans le MÊME répertoire, puis renommé par-dessus. Un téléchargement
interrompu, un disque plein ou un Ctrl-C laissent donc le binaire en place et
fonctionnel — jamais un fichier à moitié écrit.

  --dry-run   dit ce qui serait téléchargé et où, sans rien écrire.
  --force     passe outre les deux refus ci-dessous.

Deux refus, tous deux levés par --force :

  · un binaire compilé localement (« dev ») contient presque toujours PLUS que
    la dernière release ; l'écraser serait un retour en arrière silencieux,
    donc il faut le demander explicitement ;
  · une version déjà installée n'est pas réinstallée.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			dest, err := upgradeDestination()
			if err != nil {
				return err
			}
			return upgradeTo(cmd.Context(), cmd.OutOrStdout(), cmd.Root().Version, dest, dryRun, force)
		},
	}

	c.Flags().BoolVar(&dryRun, "dry-run", false, "montre ce qui serait fait, sans rien écrire")
	c.Flags().BoolVar(&force, "force", false, "réinstalle même depuis un build « dev » ou à version égale")
	return c
}

// upgradeDestination resolves the file this command is allowed to overwrite:
// the running binary itself.
//
// EvalSymlinks matters. A poste that installed pvecli through a symlink —
// Homebrew's bin, or a ~/.local/bin entry pointing into a versioned
// directory — would otherwise have its LINK replaced by a regular file, and
// the real binary left stale behind it. Following the link means we replace
// what actually executes.
func upgradeDestination() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", &exitError{code: pve.ExitGeneric,
			msg: "impossible de localiser le binaire en cours d'exécution : " + err.Error()}
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Not fatal: a path that cannot be resolved is still a path we can
		// try to write to. Failing here would refuse an upgrade for a reason
		// the operator can do nothing about.
		return exe, nil
	}
	return resolved, nil
}

// upgradeAsset names the release asset for the platform this binary was
// built for, and refuses — rather than guessing — for anything the release
// workflow does not publish.
//
// The two supported targets are not a preference: they are exactly what
// .github/workflows/release.yml builds and what install.sh accepts. The
// message points at the same escape hatch install.sh offers, in the same
// words, so the two never send an operator down different paths.
func upgradeAsset(tag string) (string, error) {
	target := runtime.GOOS + "/" + runtime.GOARCH
	switch target {
	case "darwin/arm64", "linux/amd64":
		return fmt.Sprintf("pvecli_%s_%s_%s", tag, runtime.GOOS, runtime.GOARCH), nil
	}
	return "", &exitError{code: pve.ExitUsage, msg: fmt.Sprintf(
		"plateforme non publiée : %s\n\n"+
			"  Les releases couvrent linux/amd64 et darwin/arm64. Pour tout le reste, la\n"+
			"  compilation depuis les sources prend une commande :\n\n"+
			"    git clone https://github.com/%s.git && cd pvecli && make install", target, updateRepo)}
}

// upgradeTo is the whole command, with its destination injected rather than
// discovered, so a test can exercise it against a temporary file instead of
// the binary running the test.
func upgradeTo(ctx context.Context, out io.Writer, installed, dest string, dryRun, force bool) error {
	// Both refusals happen BEFORE any network call: a command that declines
	// to do anything should not spend two seconds and a GitHub quota unit
	// finding that out.
	if installed == "dev" && !force {
		return &exitError{code: pve.ExitUsage,
			msg: "ce binaire est compilé localement (dev) : il contient probablement plus que la\n" +
				"dernière release, et l'écraser serait un retour en arrière.\n" +
				"  « pvecli upgrade --force » pour installer quand même la release publiée,\n" +
				"  « make install » pour réinstaller depuis les sources."}
	}

	tag, err := fetchLatestTagWithin(ctx, upgradeTagTimeout)
	if err != nil {
		return &exitError{code: pve.ExitGeneric,
			msg: "impossible de savoir quelle est la dernière release : " + err.Error()}
	}

	if tag == installed && !force {
		_, _ = fmt.Fprintf(out, "pvecli est déjà à jour (%s)\n", installed)
		return nil
	}

	asset, err := upgradeAsset(tag)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("%s/%s/releases/download/%s", githubDownloadBase, updateRepo, tag)

	if dryRun {
		_, _ = fmt.Fprintf(out, "%s → %s\n", installed, tag)
		_, _ = fmt.Fprintf(out, "  téléchargerait  %s/%s\n", base, asset)
		_, _ = fmt.Fprintf(out, "  vérifierait     %s/%s\n", base, upgradeSums)
		_, _ = fmt.Fprintf(out, "  remplacerait    %s\n", dest)
		_, _ = fmt.Fprintln(out, "  (--dry-run : rien n'a été écrit)")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, upgradeDownloadTimeout)
	defer cancel()

	binary, err := fetchAsset(ctx, base+"/"+asset)
	if err != nil {
		return &exitError{code: pve.ExitGeneric, msg: "téléchargement de " + asset + " : " + err.Error()}
	}
	sums, err := fetchAsset(ctx, base+"/"+upgradeSums)
	if err != nil {
		return &exitError{code: pve.ExitGeneric, msg: "téléchargement de " + upgradeSums + " : " + err.Error()}
	}

	want, err := sumFor(string(sums), asset)
	if err != nil {
		return &exitError{code: pve.ExitGeneric, msg: err.Error()}
	}
	got := sha256.Sum256(binary)
	if hex.EncodeToString(got[:]) != want {
		return &exitError{code: pve.ExitGeneric, msg: fmt.Sprintf(
			"SOMME DE CONTRÔLE INCORRECTE — rien n'a été installé.\n"+
				"  attendue %s\n  obtenue  %s\n"+
				"Les octets reçus ne sont pas ceux qui ont été publiés. Ne réessaie pas en\n"+
				"boucle : recharge la page de la release et vérifie ce qui s'y trouve.", want, hex.EncodeToString(got[:]))}
	}

	if err := replaceBinary(dest, binary); err != nil {
		return err
	}

	// The shell hook reads this cache, not the binary (cmd/update.go). Left
	// in place, it would keep announcing an upgrade that has just been taken,
	// for up to 24h — the one moment where a stale cache is guaranteed wrong.
	_ = os.Remove(updateCachePath())

	_, _ = fmt.Fprintf(out, "pvecli %s → %s installé — %s\n", installed, tag, dest)
	return nil
}

// fetchAsset downloads one release asset in full. Release assets are served
// through a redirect to a CDN; http.Client follows it by default.
func fetchAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub a répondu %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// sumFor extracts the expected checksum of one asset from a SHA256SUMS file.
//
// An asset missing from the file is an error, never a skipped verification:
// "I could not find the sum" and "the sum matched" must not share a code
// path, or the whole check becomes optional the day a release is published
// with an incomplete SHA256SUMS.
func sumFor(sums, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// shasum writes "sum  name", and prefixes the name with '*' in binary
		// mode. Both spellings name the same file.
		if strings.TrimPrefix(fields[1], "*") == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s ne contient aucune somme pour %s — publication incomplète, rien n'a été installé", upgradeSums, asset)
}

// replaceBinary writes the new binary next to the old one and renames it
// over it.
//
// Same directory, not os.TempDir(): a rename across filesystems fails, and
// ~/.local/bin and /tmp are routinely on different ones. The temp file is
// removed on every failure path — a botched upgrade must not leave a
// half-written binary lying next to the real one.
func replaceBinary(dest string, content []byte) error {
	dir := filepath.Dir(dest)

	tmp, err := os.CreateTemp(dir, ".pvecli-upgrade-*")
	if err != nil {
		return upgradeWriteError(dest, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeded

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return upgradeWriteError(dest, err)
	}
	if err := tmp.Close(); err != nil {
		return upgradeWriteError(dest, err)
	}
	// 0755 and not the CreateTemp default of 0600: this file is about to
	// become an executable, and one that only its owner can read is not the
	// one that was replaced.
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return upgradeWriteError(dest, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return upgradeWriteError(dest, err)
	}
	return nil
}

// upgradeWriteError turns a filesystem failure into the sentence an operator
// can act on. A permission denial here is not a bug, it is an installation
// under a root-owned prefix — the single most likely failure of this whole
// command, and the one where a bare "permission denied" helps least.
func upgradeWriteError(dest string, err error) error {
	if os.IsPermission(err) {
		return &exitError{code: pve.ExitGeneric, msg: fmt.Sprintf(
			"écriture refusée dans %s : %v\n\n"+
				"  Le binaire est installé dans un répertoire qui ne t'appartient pas.\n"+
				"    sudo pvecli upgrade            remplace-le en tant que root\n"+
				"    make install PREFIX=$HOME/.local   réinstalle dans ton propre répertoire",
			filepath.Dir(dest), err)}
	}
	return &exitError{code: pve.ExitGeneric,
		msg: fmt.Sprintf("remplacement de %s impossible : %v", dest, err)}
}
