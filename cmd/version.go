package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd reads the version of the *node*.
//
// The binary's own version is the --version flag. Most CLIs conflate the two;
// here they cannot be, because they fail for entirely different reasons:
// --version answers offline with no token, this one needs the network, a
// verified TLS chain and valid credentials.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Affiche la version du nœud Proxmox VE (GET /version)",
		Long: `Interroge le nœud configuré et affiche sa version, sa release et son repoid.

À ne pas confondre avec « pvecli --version », qui affiche la version de ce
binaire. La version du nœud conditionne le schéma de tous les endpoints : un
endpoint « qui n'existe pas » est très souvent un endpoint d'une autre version.
Elle est donc mémorisée dans le contexte courant, sous « detected_version ».

Endpoint : GET /api2/json/version`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			v, err := client.Version(cmd.Context())
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "PVE %s (release %s, repoid %s)\n",
				v.Version, v.Release, v.RepoID)

			// Remembering the version is a convenience, not the point of the
			// command: a read-only config must not turn a successful read into
			// a failure.
			if _, err := writeKey(cmd, "detected_version", v.Version); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"note : version non mémorisée dans la configuration (%v)\n", err)
			}
			return nil
		},
	}
}
