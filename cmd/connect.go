package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dev-toolings/pvecli/internal/catalog"
	"github.com/dev-toolings/pvecli/internal/iac"
	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/secret"
)

// Indirected so a test can exercise both branches without writing into the
// operator's real keychain — which is exactly the kind of side effect a test
// suite must not have on the machine it runs on.
var (
	secretAvailable = secret.Available
	secretStore     = secret.Store
)

// reportConnections turns what the roles published into the block that answers
// "and now, how do I get in?".
//
// It is the last thing a run prints, and the only part of the output an
// operator will still need tomorrow.
func reportConnections(cmd *cobra.Command, inv *iac.Inventory, outDir string) error {
	published, err := iac.ReadOutputs(outDir)
	if err != nil {
		return err
	}
	if len(published) == 0 {
		// A playbook with no service role publishes nothing. That is a run, not
		// a failure — and printing an empty block would suggest otherwise.
		return nil
	}

	secretKeys, err := secretKeysFromCatalog()
	if err != nil {
		return err
	}

	errW := cmd.ErrOrStderr()
	conns := make([]output.Connection, 0, len(published))
	for _, p := range published {
		conn := output.Connection{Host: p.Host}
		for _, h := range inv.Hosts {
			if h.Name == p.Host {
				conn.IP, conn.User = h.IP, h.User
				break
			}
		}

		for key, value := range p.Values {
			if value == "" {
				continue
			}
			if !secretKeys[key] {
				conn.Entries = append(conn.Entries, output.Entry{Key: key, Value: value})
				continue
			}

			ref := secret.Ref{Service: "pvecli-" + p.Host, Account: key}
			if secretAvailable() {
				if err := secretStore(ref, value); err != nil {
					return err
				}
				conn.Entries = append(conn.Entries, output.Entry{
					Key: key, Secret: true, Value: "→ trousseau : " + ref.ReadCommand(),
				})
				continue
			}
			// No keychain to write to. Saying where the value is not is worse
			// than useless, so it is shown once, on stderr, where `-o json`
			// will not capture it.
			_, _ = fmt.Fprintf(errW,
				"\n⚠ %s / %s — aucun trousseau sur cette plateforme, valeur affichée UNE fois :\n  %s\n",
				p.Host, key, value)
			conn.Entries = append(conn.Entries, output.Entry{
				Key: key, Secret: true, Value: "affiché une fois sur stderr — non conservé",
			})
		}
		conns = append(conns, conn)
	}

	_, _ = fmt.Fprintf(errW, "\naccès aux services installés :\n")

	opts, err := renderOptions(cmd)
	if err != nil {
		return err
	}
	return output.Render(cmd.OutOrStdout(), opts, conns, output.ConnectRows(conns))
}

// secretKeysFromCatalog is the single source of truth for what must never be
// rendered. Marking a value secret at the point it is printed would mean every
// new call site has to remember; marking it in the catalogue means the role
// that produces it declares it once.
func secretKeysFromCatalog() (map[string]bool, error) {
	cat, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, s := range cat.Services {
		for _, o := range s.Outputs {
			if o.Secret {
				keys[o.Key] = true
			}
		}
	}
	return keys, nil
}
