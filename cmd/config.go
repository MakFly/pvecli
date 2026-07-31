package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/MakFly/pvectl/internal/config"
	"github.com/spf13/cobra"
)

// configPath resolves which file the config commands act on.
func configPath(cmd *cobra.Command) (string, error) {
	flagValue, _ := cmd.Flags().GetString("config")
	return config.Path(flagValue)
}

// resolveConfig applies the layering of PRD §7.1 for the given command.
//
// Every command that needs a target calls this. There is deliberately no
// PersistentPreRun stashing a global: no hidden state means each command can
// be tested on its own, and the config file is read exactly when it is needed.
func resolveConfig(cmd *cobra.Command) (*config.Effective, error) {
	path, err := configPath(cmd)
	if err != nil {
		return nil, err
	}
	f, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return config.Resolve(cmd.Flags(), f)
}

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Gère le fichier de configuration local",
		Long: `Crée, affiche et modifie ~/.config/pvectl/config.yaml.

La configuration effective se résout par couches, la première qui répond gagne :

  1. flags            --endpoint, --token-id, --node, --context
  2. environnement    ` + config.EnvEndpoint + `, ` + config.EnvTokenID + `, ` + config.EnvInsecure + `
  3. fichier          le contexte courant de config.yaml
  4. défauts

Le secret du token échappe à cette règle : il ne vient que de ` + config.EnvTokenSecret + `.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newConfigInitCmd(), newConfigShowCmd(), newConfigSetCmd(), newConfigTrustCmd())
	return c
}

func newConfigInitCmd() *cobra.Command {
	var force bool

	c := &cobra.Command{
		Use:   "init",
		Short: "Crée le fichier de configuration",
		Long: `Crée le fichier de configuration avec les droits 0600, dans un dossier 0700.

Les valeurs du contexte sont amorcées par le même layering que le reste de la
CLI — un flag l'emporte sur l'environnement :

  pvectl config init --endpoint https://pve.example:8006 --node pve
  ` + config.EnvEndpoint + `=https://pve.example:8006 pvectl config init

Le contexte créé s'appelle « lab » sauf indication de --context.
Le secret du token n'est jamais écrit dans le fichier.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath(cmd)
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s existe déjà — relance avec --force pour l'écraser", path)
			}

			// Seed from an empty File so only flags and environment feed the
			// new context: an init must not inherit from a file it replaces.
			seed, err := config.Resolve(cmd.Flags(), &config.File{})
			if err != nil {
				return err
			}
			name := seed.ContextName
			if name == "" {
				name = "lab"
			}

			f := &config.File{
				CurrentContext: name,
				Contexts: map[string]*config.Context{
					name: {
						Endpoint: seed.Endpoint,
						TokenID:  seed.TokenID,
						Node:     seed.Node,
						TLS:      seed.TLS,
					},
				},
			}
			if err := config.Save(path, f); err != nil {
				return err
			}

			// Progress goes to stderr: stdout carries data only (PRD §7.4).
			// A failed write to stderr leaves nothing useful to do about it.
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "configuration écrite dans %s (0600), contexte « %s »\n", path, name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "écrase un fichier existant")
	return c
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Affiche la configuration effective et l'origine de chaque valeur",
		Long: `Affiche la configuration telle que les commandes la verront — après
résolution des couches, pas le contenu brut du fichier. La colonne de droite
donne la couche gagnante, ce qui rend le layering observable :

  pvectl config show
  ` + config.EnvEndpoint + `=https://autre:8006 pvectl config show

Le secret du token n'est jamais affiché, seulement sa présence.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			line := func(key, value, source string) {
				if value == "" {
					value = "—"
				}
				// Writes land in the tabwriter's buffer; the real error, if
				// any, surfaces on Flush below, which is checked.
				_, _ = fmt.Fprintf(w, "%s\t%s\t(%s)\n", key, value, source)
			}

			line("contexte", eff.ContextName, eff.Sources["contexte"])
			line("endpoint", eff.Endpoint, eff.Sources["endpoint"])
			line("token_id", eff.TokenID, eff.Sources["token_id"])
			line("node", eff.Node, eff.Sources["node"])
			line("insecure", strconv.FormatBool(eff.Insecure), eff.Sources["insecure"])
			if eff.TLS.Fingerprint != "" {
				line("tls.fingerprint", eff.TLS.Fingerprint, eff.Sources["tls.fingerprint"])
			}
			if eff.TLS.CAFile != "" {
				line("tls.ca_file", eff.TLS.CAFile, eff.Sources["tls.ca_file"])
			}

			// Always shown, even empty: an operator debugging « iac state ne
			// trouve rien » needs to see that terraform_dir is unset, and an
			// omitted line looks like a value that is fine.
			line("iac.terraform_dir", eff.IaC.TerraformDir, eff.Sources["iac.terraform_dir"])
			line("iac.ansible_dir", eff.IaC.AnsibleDir, eff.Sources["iac.ansible_dir"])
			line("iac.managed_tag", eff.IaC.ManagedTag, eff.Sources["iac.managed_tag"])

			// Presence, never the value — not even a prefix.
			if eff.TokenSecret != "" {
				line("token_secret", "<défini>", "env "+config.EnvTokenSecret)
			} else {
				line("token_secret", "<non défini>", "à exporter dans "+config.EnvTokenSecret)
			}

			return w.Flush()
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <clé> <valeur>",
		Short: "Modifie une clé du contexte courant",
		Long: `Écrit une clé dans le contexte courant du fichier de configuration.

Clés acceptées : ` + strings.Join(config.WritableKeys, ", ") + `

  pvectl config set endpoint https://pve.example:8006
  pvectl config set tls.fingerprint 9F:3D:1A:55:...

« token_secret » est refusé : ce n'est pas un oubli, voir le message d'erreur.`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			name, err := writeKey(cmd, key, value)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s = %s (contexte « %s »)\n", key, value, name)
			return nil
		},
	}
}

// writeKey stores one key in the current context of the config file and
// returns the context it landed in. Shared by `config set` and `config trust`.
func writeKey(cmd *cobra.Command, key, value string) (string, error) {
	path, err := configPath(cmd)
	if err != nil {
		return "", err
	}
	f, err := config.Load(path)
	if err != nil {
		return "", err
	}

	name := f.CurrentContext
	if v, _ := cmd.Flags().GetString("context"); v != "" {
		name = v
	}
	if name == "" {
		return "", fmt.Errorf("aucun contexte courant dans %s — lance d'abord « pvectl config init »", path)
	}
	target, ok := f.Contexts[name]
	if !ok || target == nil {
		return "", fmt.Errorf("contexte %q introuvable dans %s", name, path)
	}

	if err := config.SetKey(target, key, value); err != nil {
		return "", err
	}
	return name, config.Save(path, f)
}
