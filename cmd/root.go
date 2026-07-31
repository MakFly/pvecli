// Package cmd holds the Cobra command tree: flag parsing, help and shell
// completion. It never performs an HTTP call itself — commands delegate to
// internal/service, which delegates to internal/pve (PRD §5.1).
package cmd

import (
	"fmt"

	"github.com/MakFly/pvecli/internal/config"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the whole command tree. It takes the build metadata as
// arguments rather than reading a package-level variable, so tests can render
// a deterministic --version output.
func NewRootCmd(version, commit string) *cobra.Command {
	root := &cobra.Command{
		Use:   "pvecli",
		Short: "CLI d'administration Proxmox VE",
		Long: `pvecli pilote un nœud Proxmox VE via son API REST (/api2/json).

Authentification par token d'API, TLS vérifié, sortie table|json|yaml.

  pvecli --version    version de ce binaire
  pvecli version      version du nœud PVE interrogé`,

		// No Run/RunE: with no argument Cobra prints the help and returns nil,
		// which main turns into exit code 0.
		Args: usage(cobra.NoArgs),

		// Print usage only for usage errors, not for runtime failures — a
		// failed API call should not dump the whole help.
		SilenceUsage: true,
	}

	// Declaring the flag before setting Version keeps Cobra from claiming the
	// -v shorthand for it: -v / -vv belong to --verbose (PVX-009).
	root.Flags().Bool("version", false, "version de ce binaire (pas celle du nœud PVE)")
	root.Version = version
	root.SetVersionTemplate(fmt.Sprintf("pvecli %s (commit %s)\n", version, commit))

	// Persistent, so every present and future subcommand inherits them. These
	// are the top layer of the resolution order in PRD §7.1; --insecure and
	// --timeout join them with PVX-004 and PVX-003.
	root.PersistentFlags().String("config", "", "chemin du fichier de configuration")
	root.PersistentFlags().String("context", "", "contexte à utiliser (défaut : current_context)")
	root.PersistentFlags().String("endpoint", "", "URL de l'API PVE ("+config.EnvEndpoint+")")
	root.PersistentFlags().String("token-id", "", "identifiant du token ("+config.EnvTokenID+")")
	root.PersistentFlags().String("node", "", "nœud PVE par défaut")
	root.PersistentFlags().Duration("timeout", pve.DefaultTimeout, "budget par requête HTTP")
	root.PersistentFlags().CountP("verbose", "v", "trace HTTP sur stderr ; -vv ajoute en-têtes et corps")
	root.PersistentFlags().Bool("insecure", false, "désactive la vérification TLS ("+config.EnvInsecure+") — avertit à chaque appel")

	// A mistyped flag is a usage error, not a generic failure (PRD §7.5).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{code: pve.ExitUsage, msg: err.Error()}
	})

	root.AddCommand(
		newVersionCmd(), newConfigCmd(), newNodeCmd(), newDoctorCmd(),
		newVMCmd(), newLXCCmd(), newGuestCmd(), newStorageCmd(), newTaskCmd(), newClusterCmd(),
		newAccessCmd(), newBackupCmd(), newDRCmd(), newIaCCmd(),
		newNetCmd(), newPoolCmd(), newAICmd(),
	)

	// Last, and on the built tree: the completers are attached by walking the
	// commands rather than by annotating each constructor, so a command added
	// later is served without anyone remembering to wire it (PVX-053).
	wireCompletion(root)

	return root
}

// Execute builds and runs the command tree. The returned error has already
// been reported to stderr by Cobra.
func Execute(version, commit string) error {
	return NewRootCmd(version, commit).Execute()
}
