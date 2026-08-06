package cmd

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dev-toolings/pvecli/internal/iac"
	"github.com/spf13/cobra"
)

// ensureTool looks for a tool pvecli wraps and, if it is missing, offers —
// once, and only after confirmation — to install it through whatever native
// package manager is already on the machine's PATH.
//
// Without --yes and without a terminal to ask on, this changes nothing:
// confirm fails immediately and the original MissingToolError, hint included,
// reaches the operator exactly as it did before this existed. That is
// deliberate — a CI log must not start seeing a different error, or worse,
// start attempting sudo, just because this function was added.
func ensureTool(cmd *cobra.Command, tool iac.Tool) error {
	lookErr := tool.Look()
	var missing *iac.MissingToolError
	if lookErr == nil || !errors.As(lookErr, &missing) {
		return lookErr
	}

	pm, ok := iac.DetectPackageManager()
	if !ok {
		return lookErr
	}
	argv := pm.Install(iac.PackageName(tool.Name))

	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		question := fmt.Sprintf("« %s » est introuvable. L'installer avec « %s » ?", tool.Name, strings.Join(argv, " "))
		if err := confirm(cmd, question); err != nil {
			return lookErr
		}
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n$ %s\n\n", strings.Join(argv, " "))
	install := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...) //nolint:gosec // argv comes from this package's own constants, never user input
	install.Stdout, install.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	install.Stdin = cmd.InOrStdin() // sudo may need to prompt for a password
	if err := install.Run(); err != nil {
		// The package manager already explained itself (e.g. apt's "Unable to
		// locate package terraform" when HashiCorp's repo isn't configured).
		// A second wording would only compete with the first — the original
		// MissingToolError and its manual hint are what's still actionable.
		return lookErr
	}

	return tool.Look()
}
