package cmd

import (
	"fmt"
	"os"

	"github.com/MakFly/pvecli/internal/config"
	"github.com/MakFly/pvecli/internal/log"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

// newClient builds an API client from the effective configuration.
//
// It is the single door to the network: every command that talks to the node
// goes through here, which is what makes the --insecure warning impossible to
// forget in one command and not another.
func newClient(cmd *cobra.Command) (*pve.Client, error) {
	eff, err := resolveConfig(cmd)
	if err != nil {
		return nil, err
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")
	verbosity, _ := cmd.Flags().GetCount("verbose")

	// A Cloudflare Access application in front of the node turns away anything
	// that is not a browser. These two headers are how a CLI gets through, and
	// like every other secret here they come from the environment only.
	accessID := os.Getenv(config.EnvAccessClientID)
	accessSecret := os.Getenv(config.EnvAccessClientSecret)

	// The tracer is told the secrets so it can blank them wherever they might
	// surface — in a body, in a redirect URL, in an error echoed by the node.
	tracer := log.New(cmd.ErrOrStderr(), log.LevelFor(verbosity), eff.TokenSecret, accessSecret)

	client, err := pve.New(pve.Options{
		Endpoint:           eff.Endpoint,
		TokenID:            eff.TokenID,
		Secret:             eff.TokenSecret,
		Timeout:            timeout,
		AccessClientID:     accessID,
		AccessClientSecret: accessSecret,
		Trace:              traceOrNil(tracer),
		Trust: pve.TrustOptions{
			Fingerprint: eff.TLS.Fingerprint,
			CAFile:      eff.TLS.CAFile,
			Insecure:    eff.Insecure,
		},
	})
	if err != nil {
		return nil, err
	}

	if client.TrustMode() == pve.TrustNone {
		// On stderr, at every single invocation, and never on stdout: the
		// warning has to be impossible to miss for a human and impossible to
		// swallow for `| jq`. A mode that is comfortable to use is a mode that
		// becomes the habit — which is the actual risk being managed here.
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"AVERTISSEMENT : vérification TLS désactivée (--insecure). "+
				"La connexion à %s n'est pas authentifiée.\n"+
				"Épingle plutôt l'empreinte, une fois : pvecli config trust\n",
			eff.Endpoint)
	}

	return client, nil
}

// traceOrNil keeps a disabled tracer out of the client entirely, so the hot
// path costs nothing when --verbose is absent. Returning the concrete type
// would give the client a non-nil interface holding a nil-behaviour value.
func traceOrNil(t *log.Tracer) pve.Tracer {
	if !t.Enabled() {
		return nil
	}
	return t
}
