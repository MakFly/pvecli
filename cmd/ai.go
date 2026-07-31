package cmd

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// proxmoxAgent is the subagent definition, compiled into the binary.
//
// Embedded rather than shipped alongside, for the same reason the binary is
// built static: `pvecli ai install` must work from a single file copied onto a
// machine, with nothing else to fetch. The definition and the CLI it drives are
// then versioned together — an agent that documents flags the local binary does
// not have is worse than no agent.
//
//go:embed assets/proxmox-ops.md
var proxmoxAgent string

// agentFileName is the name Claude Code discovers the agent under. It must
// match the `name:` field of the front matter above.
const agentFileName = "proxmox-ops.md"

func newAICmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ai",
		Short: "Installe l'agent IA dédié à Proxmox (Claude Code)",
		Long: `Installe, dans la configuration GLOBALE de Claude Code, un sous-agent qui sait
piloter cette CLI.

  ~/.claude/agents/` + agentFileName + `

L'agent connaît ce que l'aide en ligne ne dit pas : la règle « une acceptation
n'est pas un résultat », la garde de propriété du tag « managed », les refus
volontaires (Sys.Modify, Permissions.Modify), les plages de VMID réservées, et
la liste des pièges qui ont réellement coûté du temps sur ce lab — 8192 et non
8, l'agent invité absent qui fait durer un apply douze minutes, le 200 OK de la
page par défaut de Debian.

C'est la même chaîne qu'à la main, conduite par un agent :

  pvecli doctor → édition du main.tf → iac plan → iac apply → iac configure

La définition est COMPILÉE DANS LE BINAIRE. Elle suit donc la version de pvecli
qu'elle décrit, et « pvecli ai install » ne télécharge rien.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newAIInstallCmd(), newAIPrintCmd(), newAIStatusCmd())
	return c
}

// agentDir resolves where Claude Code looks for globally-installed agents.
//
// CLAUDE_CONFIG_DIR is honoured because Claude Code honours it: a user who
// moved their configuration would otherwise get an agent installed where
// nothing reads it, and a silent no-op is the worst kind of success.
func agentDir(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "agents"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("impossible de déterminer le dossier personnel : %w", err)
	}
	return filepath.Join(home, ".claude", "agents"), nil
}

// agentState is what is currently on disk at the target path.
type agentState int

const (
	agentAbsent agentState = iota
	agentCurrent
	agentModified
)

// inspectAgent compares the installed file with the embedded definition.
//
// Byte equality, not a version stamp: a version number only tells you what the
// file claimed to be, never whether someone edited it since. The distinction
// matters because install refuses to overwrite local edits.
func inspectAgent(path string) (agentState, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return agentAbsent, nil
	}
	if err != nil {
		return agentAbsent, fmt.Errorf("lecture de %s : %w", path, err)
	}
	if string(raw) == proxmoxAgent {
		return agentCurrent, nil
	}
	return agentModified, nil
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func newAIInstallCmd() *cobra.Command {
	var dir string
	var force bool

	c := &cobra.Command{
		Use:   "install",
		Short: "Écrit l'agent dans ~/.claude/agents/",
		Long: `Écrit la définition de l'agent dans la configuration globale de Claude Code.

Le fichier cible est créé, jamais fusionné. Si le fichier présent DIFFÈRE de la
définition embarquée, la commande s'arrête : la différence est soit une
personnalisation que tu as écrite, soit une version antérieure, et écraser
silencieusement l'une ou l'autre ne rend service à personne. --force tranche,
après que tu aies regardé (« pvecli ai print » donne la version embarquée).

Rien n'est envoyé sur le réseau, et l'agent n'a besoin d'aucune clé d'API :
c'est Claude Code qui l'exécute, avec les identifiants dont il dispose déjà.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := agentDir(dir)
			if err != nil {
				return err
			}
			path := filepath.Join(base, agentFileName)

			state, err := inspectAgent(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			switch state {
			case agentCurrent:
				_, _ = fmt.Fprintf(out, "déjà installé et à jour — %s\n", path)
				return nil
			case agentModified:
				if !force {
					return fmt.Errorf(`%s existe et diffère de la définition embarquée.

  sur disque  %s
  embarquée   %s

Regarde avant d'écraser :

  pvecli ai print > /tmp/%s && diff %s /tmp/%s

puis « pvecli ai install --force » si tu assumes la perte des modifications`,
						path, digest(mustRead(path)), digest(proxmoxAgent),
						agentFileName, path, agentFileName)
				}
			}

			if err := os.MkdirAll(base, 0o755); err != nil {
				return fmt.Errorf("création de %s : %w", base, err)
			}
			if err := os.WriteFile(path, []byte(proxmoxAgent), 0o644); err != nil {
				return fmt.Errorf("écriture de %s : %w", path, err)
			}

			_, _ = fmt.Fprintf(out, "agent « proxmox-ops » installé — %s\n", path)
			_, _ = fmt.Fprintln(out, `
Dans Claude Code, il se déclenche sur une demande d'infrastructure Proxmox, ou
s'invoque explicitement :

  > utilise l'agent proxmox-ops pour créer une VM 4 vCPU 16 Go

Une session Claude Code déjà ouverte doit être relancée pour le voir.`)
			return nil
		},
	}

	c.Flags().StringVar(&dir, "dir", "", "répertoire d'agents (défaut : ~/.claude/agents)")
	c.Flags().BoolVar(&force, "force", false, "écrase un fichier modifié localement")
	return c
}

// mustRead is only reached on a path os.ReadFile already succeeded on, in the
// error message of install. A read that fails here degrades the message rather
// than the command.
func mustRead(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func newAIPrintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print",
		Short: "Écrit la définition de l'agent sur la sortie standard",
		Long: `Rend la définition embarquée, sans rien écrire sur le disque.

Utile pour la relire, la comparer à un fichier déjà installé, ou l'installer
ailleurs qu'à l'emplacement par défaut :

  pvecli ai print > .claude/agents/` + agentFileName + `

Un agent posé dans le dépôt plutôt que dans ~/.claude n'est visible que depuis
ce dépôt — c'est le bon choix si tu veux l'adapter à un nœud particulier sans
toucher à l'agent global.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), proxmoxAgent)
			return err
		},
	}
}

func newAIStatusCmd() *cobra.Command {
	var dir string

	c := &cobra.Command{
		Use:   "status",
		Short: "Dit si l'agent est installé, et s'il correspond à ce binaire",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := agentDir(dir)
			if err != nil {
				return err
			}
			path := filepath.Join(base, agentFileName)
			state, err := inspectAgent(path)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "chemin      %s\n", path)
			_, _ = fmt.Fprintf(out, "embarquée   %s\n", digest(proxmoxAgent))

			switch state {
			case agentAbsent:
				_, _ = fmt.Fprintln(out, "état        absent — « pvecli ai install » l'écrit")
			case agentCurrent:
				_, _ = fmt.Fprintln(out, "état        à jour")
			case agentModified:
				_, _ = fmt.Fprintf(out, "sur disque  %s\n", digest(mustRead(path)))
				_, _ = fmt.Fprintln(out, "état        diffère — personnalisé, ou écrit par une autre version")
			}
			return nil
		},
	}

	c.Flags().StringVar(&dir, "dir", "", "répertoire d'agents (défaut : ~/.claude/agents)")
	return c
}
