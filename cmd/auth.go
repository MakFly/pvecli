package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/secret"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newAuthCmd groups what a human does with the token secret.
//
// It exists because the answer to "the CLI says my secret is missing" used to
// be a paragraph of shell to copy. A credential you must re-paste at every new
// shell is a credential that ends up in a dotfile — the config-file outcome the
// no-secret-in-the-file rule was written to prevent, minus the supervision.
func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Gère le secret du token d'API",
		Long: `Range et retrouve le secret du token, sans jamais l'écrire dans le fichier
de configuration ni le faire passer par la ligne de commande.

Trois sources sont consultées, dans cet ordre :

  1. env ` + secret.EnvSecret + `
  2. une commande dont la sortie standard EST le secret
     (` + secret.EnvSecretCommand + `, ou « secret_command » dans le contexte)
  3. le trousseau du système (libsecret sous Linux, Keychain sous macOS)

« secret_source » dans le contexte restreint la recherche à une seule d'entre
elles — utile quand on veut qu'une erreur se voie au lieu d'être rattrapée en
silence par une source moins fraîche.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newAuthSetSecretCmd(), newAuthUnsetSecretCmd(), newAuthStatusCmd())
	return c
}

func newAuthSetSecretCmd() *cobra.Command {
	var stdin bool

	c := &cobra.Command{
		Use:   "set-secret",
		Short: "Range le secret du token dans le trousseau du système",
		Long: `Lit le secret sur l'entrée standard, ou le demande sans écho, puis l'écrit
dans le trousseau sous le nom du contexte courant.

Le secret n'est jamais accepté en argument : un argument est visible dans
« ps » par tous les utilisateurs de la machine, et reste dans l'historique du
shell. C'est la raison pour laquelle il n'existe pas de --token-secret, et
elle vaut ici aussi.

  pvecli auth set-secret                       demande la saisie
  pass show pve/token | pvecli auth set-secret --stdin`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}

			value, err := readSecret(cmd, stdin)
			if err != nil {
				return err
			}
			if value == "" {
				return errors.New("secret vide — rien n'a été écrit")
			}

			if err := secret.StoreToken(eff.ContextName, value); err != nil {
				if errors.Is(err, secret.ErrWriteUnsupported) {
					return fmt.Errorf("%w\n\n%s", err, secret.WriteHint(eff.ContextName))
				}
				return err
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "secret rangé dans le trousseau pour le contexte « %s »\n", eff.ContextName)

			// A secret in the keyring that the environment shadows is a trap:
			// it works today because of the export, and breaks in the next
			// shell for reasons nobody will connect to this command.
			if os.Getenv(secret.EnvSecret) != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"note : %s est exporté dans ce shell et garde la priorité.\n"+
						"      Le trousseau ne servira qu'une fois cette variable retirée.\n",
					secret.EnvSecret)
			}
			_, _ = fmt.Fprintf(out, "\nVérifie la chaîne complète :\n\n    pvecli auth status\n")
			return nil
		},
	}
	c.Flags().BoolVar(&stdin, "stdin", false, "lit le secret sur l'entrée standard au lieu de le demander")
	return c
}

func newAuthUnsetSecretCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset-secret",
		Short: "Retire du trousseau le secret du contexte courant",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			if err := secret.EraseToken(eff.ContextName); err != nil {
				return err
			}
			// Deleting what was never there succeeds — the caller asked for an
			// outcome, not for a transaction.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"trousseau nettoyé pour le contexte « %s »\n", eff.ContextName)
			return nil
		},
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Dit quelle source fournit le secret, sans jamais l'afficher",
		Long: `Interroge les trois sources et rapporte laquelle répond.

La valeur n'est jamais imprimée, ni en clair ni tronquée : un préfixe de
secret dans un scrollback de terminal reste un morceau de secret.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			_, _ = fmt.Fprintf(out, "contexte       %s\n", eff.ContextName)
			_, _ = fmt.Fprintf(out, "token_id       %s\n", orNotSet(eff.TokenID))

			restriction := "toutes les sources, dans l'ordre"
			if eff.SecretSource != "" {
				restriction = "restreint à « " + eff.SecretSource + " »"
			}
			_, _ = fmt.Fprintf(out, "secret_source  %s\n", restriction)

			switch {
			case eff.SecretErr != nil:
				_, _ = fmt.Fprintf(out, "secret         ERREUR\n")
			case eff.TokenSecret != "":
				_, _ = fmt.Fprintf(out, "secret         trouvé — %s\n", eff.Sources["token_secret"])
			default:
				_, _ = fmt.Fprintf(out, "secret         ABSENT\n")
			}

			// The keyring line comes before any early return: it is most
			// useful in exactly the case where the secret was NOT found, which
			// is when someone needs to know whether their set-secret landed.
			// It is also what shows an exported variable masking a stored one.
			if kr := secret.OpenKeyring(); kr != nil {
				stored, kerr := kr.Get(eff.ContextName)
				switch {
				case kerr != nil:
					_, _ = fmt.Fprintf(out, "trousseau      %s — injoignable : %v\n", kr.Name(), kerr)
				case stored != "":
					_, _ = fmt.Fprintf(out, "trousseau      %s — entrée présente\n", kr.Name())
				default:
					_, _ = fmt.Fprintf(out, "trousseau      %s — aucune entrée pour ce contexte\n", kr.Name())
				}
			} else {
				_, _ = fmt.Fprintf(out, "trousseau      aucun sur cette machine\n")
			}

			// The diagnosis is laid out on stdout in full; the error carries
			// the exit code so a script can branch on it, with a message short
			// enough not to repeat what was just printed.
			switch {
			case eff.SecretErr != nil:
				_, _ = fmt.Fprintf(out, "\n%v\n", eff.SecretErr)
				return &exitError{code: pve.ExitAuth, msg: "la source configurée n'a pas rendu le secret"}
			case eff.TokenSecret == "":
				_, _ = fmt.Fprintf(out, "\n%s\n", secret.MissingHint(eff.ContextName))
				return &exitError{code: pve.ExitAuth, msg: "secret du token introuvable"}
			}
			return nil
		},
	}
}

// readSecret takes the secret from stdin or from a no-echo prompt.
//
// A piped stdin is detected rather than demanded: `pass show … | pvecli auth
// set-secret` is the shape people will type, and failing it on a missing
// --stdin would be pedantry.
func readSecret(cmd *cobra.Command, forceStdin bool) (string, error) {
	fd := int(os.Stdin.Fd())
	piped := !term.IsTerminal(fd)

	if forceStdin || piped {
		// Everything, then trimmed — not a token scan. `pass show` prints the
		// secret followed by a newline, and a scan that stops at the first
		// space would silently truncate a secret that contained one.
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("lecture du secret sur l'entrée standard : %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}

	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Secret du token (saisie masquée) : ")
	raw, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("lecture du secret : %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func orNotSet(v string) string {
	if v == "" {
		return "<non défini>"
	}
	return v
}
