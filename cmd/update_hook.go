package cmd

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// updateNotifySnippet is scripts/shell/update-notify.sh's successor: the
// snippet has to live under cmd/ because go:embed cannot climb above the
// package doing the embedding (no ".." in the pattern) — see cmd/ai.go:24
// for the same constraint already at work with the Claude Code agent.
//
// Content unchanged by the move: it carries comments documenting two bugs
// already paid for in production (stdout swallowed, and a stale pvecli's
// "unknown flag" reaching every prompt) — see cmd/assets/update-notify.sh.
//
//go:embed assets/update-notify.sh
var updateNotifySnippet string

// shellHookSnippetFileName is the name the snippet is written under once
// copied out of the binary, so the sourced line in ~/.zshrc / ~/.bashrc has
// something stable to point at.
const shellHookSnippetFileName = "update-notify.sh"

// shellHookBegin and shellHookEnd bound the block install-hook adds to a
// shell startup file. Reused byte for byte from scripts/shell/install-hook.sh
// (now removed): a poste that already has a block wired by the old shell
// script must remain removable by `--uninstall` here.
const (
	shellHookBegin = "# >>> pvecli update notification >>>"
	shellHookEnd   = "# <<< pvecli update notification <<<"
)

// shellHookDataDir resolves $XDG_DATA_HOME/pvecli, or ~/.local/share/pvecli
// when XDG_DATA_HOME is unset — the same fallback rule the XDG base
// directory spec defines, and the same shape completionCachePath
// (cmd/completion.go) already applies to $XDG_CACHE_HOME.
//
// A pure function of its two inputs — never reads the environment itself —
// so a test can exercise it without touching a real $HOME.
func shellHookDataDir(home, xdgDataHome string) string {
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "pvecli")
	}
	return filepath.Join(home, ".local", "share", "pvecli")
}

// shellRCFile mirrors scripts/shell/install-hook.sh's rcfile(): it follows
// the LOGIN shell ($SHELL), never the interpreter executing this command,
// because `curl … | sh` runs under /bin/sh even for people whose
// interactive shell is zsh. Relying on the running interpreter would wire
// the wrong file for exactly the audience `curl | sh` serves.
//
// Returns "" when the shell is not recognised; the caller must not write
// anywhere in that case, only explain what to add by hand.
//
// A pure function of its inputs, for the same reason as shellHookDataDir:
// no env access, no real filesystem dependency beyond the two os.Stat calls
// needed to pick between .bashrc and .bash_profile (which a test satisfies
// entirely inside its own t.TempDir(), since home is a parameter here).
func shellRCFile(home, shellPath, zdotdir, override string) string {
	if override != "" {
		return override
	}

	switch filepath.Base(shellPath) {
	case "zsh":
		dir := zdotdir
		if dir == "" {
			dir = home
		}
		return filepath.Join(dir, ".zshrc")
	case "bash":
		// macOS launches terminals as login shells, where bash does not read
		// ~/.bashrc but ~/.bash_profile; Linux is the other way around. Aim
		// at ~/.bashrc when it exists — by far the common case — and
		// ~/.bash_profile only when it is the sole one present.
		bashrc := filepath.Join(home, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return bashrc
		}
		bashProfile := filepath.Join(home, ".bash_profile")
		if _, err := os.Stat(bashProfile); err == nil {
			return bashProfile
		}
		return bashrc
	default:
		return ""
	}
}

// shellHookSourceLine is the one line of the block that actually does
// something. It is written by shellHookBlock and read back by runInstallHook
// to tell a current block from a stale one, so the two must not drift: they
// share this function rather than each spelling the line out.
func shellHookSourceLine(snippetPath string) string {
	return fmt.Sprintf("[ -f %s ] && . %s", snippetPath, snippetPath)
}

// shellHookBlock is the exact block appended to a startup file: a leading
// blank line for separation, the two markers, and the guarded source line.
// Wording kept close to scripts/shell/install-hook.sh's, which it replaces.
func shellHookBlock(snippetPath string) string {
	lines := []string{
		"",
		shellHookBegin,
		"# Prévient à l'ouverture d'un terminal qu'une release plus récente existe.",
		"# Ne fait AUCUN appel réseau en premier plan. Retrait :",
		"#   pvecli update install-hook --uninstall",
		shellHookSourceLine(snippetPath),
		shellHookEnd,
		"",
	}
	return strings.Join(lines, "\n")
}

// removeShellHookBlock drops every line from the BEGIN marker (inclusive)
// to the END marker (inclusive), the same rule scripts/shell/install-hook.sh
// applied with awk. Marker matching, not fuzzy content matching, is the only
// way to remove exactly what was added even if the user reindented or moved
// the block since.
func removeShellHookBlock(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		if strings.Contains(line, shellHookBegin) {
			skip = true
		}
		if !skip {
			kept = append(kept, line)
		}
		if strings.Contains(line, shellHookEnd) {
			skip = false
		}
	}
	return strings.Join(kept, "\n")
}

// writeFileReplacingContent replaces path's content (creating it with perm
// if absent) without renaming anything over it.
//
// Renaming a temp file onto path would replace path itself — which, if path
// is a symlink (a dotfiles setup managed by chezmoi, stow, …), silently
// turns it into a plain file and detaches it from wherever it pointed. This
// preserves whatever path already is (its mode, and its identity as a
// symlink or a plain file) by opening it and rewriting its content in place.
//
// The new content is written to a throwaway temp file first, so a disk-full
// or permission error is caught before path's existing content is
// truncated — writing straight to path would already have destroyed the old
// content by the time such an error surfaced.
func writeFileReplacingContent(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("création de %s : %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".pvecli-rc-*")
	if err != nil {
		return fmt.Errorf("fichier temporaire dans %s : %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("écriture de %s : %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fermeture de %s : %w", tmpPath, err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("ouverture de %s : %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("écriture de %s : %w", path, err)
	}
	return f.Close()
}

func newUpdateInstallHookCmd() *cobra.Command {
	var uninstall, print bool

	c := &cobra.Command{
		Use:   "install-hook",
		Short: "Câble (ou retire) la notification de mise à jour dans le shell",
		Long: `Écrit le snippet embarqué (voir « pvecli update check --notify ») dans

  ${XDG_DATA_HOME:-$HOME/.local/share}/pvecli/update-notify.sh

puis ajoute au fichier de démarrage du shell un bloc qui le source. C'est ce
qui manque au chemin d'installation en une ligne (curl … | sh) : install.sh
n'a que le binaire qu'il vient de poser, jamais le dépôt — cette commande
rend PVX-090 accessible sans lui.

Le fichier visé suit $SHELL (le shell de CONNEXION), pas l'interpréteur qui
exécute cette commande : zsh → $ZDOTDIR/.zshrc (ou ~/.zshrc), bash → ~/.bashrc
si présent sinon ~/.bash_profile. Un shell non reconnu ne déclenche AUCUNE
écriture ; la ligne à ajouter à la main est imprimée à la place.

Rejouable : le bloc est repéré par une paire de marqueurs, jamais par une
recherche floue sur le contenu, et n'est jamais dupliqué. PVECLI_SHELL_RC
force le fichier visé ; PVECLI_NO_SHELL_HOOK=1 ne fait rien du tout.

  --print       imprime le snippet sur stdout, n'écrit rien nulle part.
  --uninstall   retire le bloc et supprime le fichier écrit ; retirer ce qui
                n'est pas là réussit — l'appelant demande un état, pas une
                transaction.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if print {
				_, err := fmt.Fprint(out, updateNotifySnippet)
				return err
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("impossible de déterminer le dossier personnel : %w", err)
			}
			rc := shellRCFile(home, os.Getenv("SHELL"), os.Getenv("ZDOTDIR"), os.Getenv("PVECLI_SHELL_RC"))
			snippetPath := filepath.Join(shellHookDataDir(home, os.Getenv("XDG_DATA_HOME")), shellHookSnippetFileName)

			if uninstall {
				return runUninstallHook(out, rc, snippetPath)
			}
			if os.Getenv("PVECLI_NO_SHELL_HOOK") == "1" {
				_, _ = fmt.Fprintln(out, "· notification non câblée (PVECLI_NO_SHELL_HOOK=1)")
				return nil
			}
			return runInstallHook(out, rc, snippetPath)
		},
	}

	c.Flags().BoolVar(&uninstall, "uninstall", false, "retire le câblage et le fichier snippet")
	c.Flags().BoolVar(&print, "print", false, "imprime le snippet sur stdout, n'écrit rien")
	return c
}

// runInstallHook wires the hook. rc == "" means the shell was not
// recognised: nothing is written anywhere, including the snippet file —
// there would be nothing valid to point the manual instructions at.
func runInstallHook(out io.Writer, rc, snippetPath string) error {
	if rc == "" {
		_, _ = fmt.Fprintf(out, "· shell non reconnu ($SHELL=%s) — notification non câblée.\n", os.Getenv("SHELL"))
		_, _ = fmt.Fprintln(out, "  À ajouter à la main dans ton fichier de démarrage :")
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintf(out, "    [ -f %s ] && . %s\n", snippetPath, snippetPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(snippetPath), 0o700); err != nil {
		return fmt.Errorf("création de %s : %w", filepath.Dir(snippetPath), err)
	}
	if err := os.WriteFile(snippetPath, []byte(updateNotifySnippet), 0o755); err != nil {
		return fmt.Errorf("écriture de %s : %w", snippetPath, err)
	}

	existing, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("lecture de %s : %w", rc, err)
	}

	// Idempotence, mais pas naïve : la présence du marqueur ne suffit pas.
	//
	// Mesuré le 03-08-2026, une heure après avoir posé le bloc à la main. Le
	// snippet a changé d'emplacement dans le dépôt ; le bloc, lui, est resté à
	// pointer sur l'ancien chemin. Sa garde `[ -f … ]` a fait exactement son
	// travail — ne rien casser — et le résultat a été une notification
	// silencieusement morte, avec un `install-hook` qui répondait « déjà
	// câblée » sans rien vérifier. Encore une fois : le bloc était là, et il
	// ne faisait rien.
	//
	// On compare donc au CHEMIN, pas au marqueur. Un bloc qui source autre
	// chose que la cible courante est périmé et se fait remplacer.
	previous := string(existing)
	if strings.Contains(previous, shellHookBegin) {
		if strings.Contains(previous, shellHookSourceLine(snippetPath)) {
			_, _ = fmt.Fprintf(out, "· notification déjà câblée dans %s\n", rc)
			return nil
		}
		previous = removeShellHookBlock(previous)
		_, _ = fmt.Fprintf(out, "· bloc obsolète remplacé dans %s\n", rc)
	}

	newContent := previous + shellHookBlock(snippetPath)
	if err := writeFileReplacingContent(rc, []byte(newContent), 0o644); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "✓ notification câblée dans %s\n", rc)
	_, _ = fmt.Fprintln(out, "  effet au prochain terminal — retrait : pvecli update install-hook --uninstall")
	return nil
}

// runUninstallHook removes the block and the snippet file. Removing what is
// not there succeeds: the caller is asking for a state (uninstalled), not
// requesting a transaction that can fail on an already-clean machine.
func runUninstallHook(out io.Writer, rc, snippetPath string) error {
	if err := os.Remove(snippetPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("suppression de %s : %w", snippetPath, err)
	}

	if rc == "" {
		_, _ = fmt.Fprintln(out, "rien à retirer (aucun fichier de démarrage connu)")
		return nil
	}

	content, err := os.ReadFile(rc)
	if errors.Is(err, fs.ErrNotExist) {
		_, _ = fmt.Fprintln(out, "rien à retirer (aucun fichier de démarrage connu)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", rc, err)
	}
	if !strings.Contains(string(content), shellHookBegin) {
		_, _ = fmt.Fprintf(out, "rien à retirer dans %s\n", rc)
		return nil
	}

	newContent := removeShellHookBlock(string(content))
	if err := writeFileReplacingContent(rc, []byte(newContent), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "✓ bloc pvecli retiré de %s\n", rc)
	return nil
}
