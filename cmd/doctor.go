package cmd

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/dev-toolings/pvecli/internal/config"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

// check is one step of the diagnostic ladder.
type check struct {
	label string
	// blocking marks a step whose failure makes every later step meaningless.
	// Diagnosing an ACL before confirming TLS is how afternoons disappear.
	blocking bool
	run      func(context.Context, *pve.Client) (string, error)
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Vérifie toute la chaîne d'accès au nœud",
		Long: `Déroule la séquence d'inspection minimale, du plus bas niveau au plus haut :

  réseau → TLS → authentification → nœud → privilèges

L'ordre n'est pas cosmétique. Diagnostiquer une ACL avant d'avoir confirmé le
TLS fait perdre des heures : la moitié des « 403 » d'un débutant sont en fait
des erreurs de certificat ou de secret. La commande s'arrête donc à la première
étape bloquante, et dit lesquelles n'ont pas été exécutées.

Endpoints : GET /version · /cluster/status · /nodes · /access/permissions`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			hasAccessToken := os.Getenv(config.EnvAccessClientID) != "" &&
				os.Getenv(config.EnvAccessClientSecret) != ""

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "cible     %s\n", eff.Endpoint)
			_, _ = fmt.Fprintf(out, "identité  %s\n", eff.TokenID)
			// Which source answered, never the value. Reaching this line means
			// a secret was found — newClient refuses earlier otherwise — so the
			// useful fact is *which* of the three gave it. The classic surprise
			// is an export in the current shell quietly masking a keyring
			// entry, and this is the only place that becomes visible.
			_, _ = fmt.Fprintf(out, "secret    %s\n", eff.Sources["token_secret"])
			_, _ = fmt.Fprintf(out, "TLS       %s\n", client.TrustMode())
			if hasAccessToken {
				_, _ = fmt.Fprintf(out, "Access    service token présent\n")
			}
			_, _ = fmt.Fprintln(out)

			checks := []check{
				{"endpoint joignable et version lue", true, func(ctx context.Context, c *pve.Client) (string, error) {
					v, err := c.Version(ctx)
					if err != nil {
						return "", err
					}
					return "PVE " + v.Version, nil
				}},
				{"cluster interrogeable", false, func(ctx context.Context, c *pve.Client) (string, error) {
					st, err := c.ClusterStatus(ctx)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("%d entrée(s)", len(st)), nil
				}},
				{"nœuds listés", true, func(ctx context.Context, c *pve.Client) (string, error) {
					nodes, err := c.Nodes(ctx)
					if err != nil {
						return "", err
					}
					names := make([]string, 0, len(nodes))
					for _, n := range nodes {
						names = append(names, fmt.Sprintf("%s (%s)", n.Node, n.Status))
					}
					return strings.Join(names, ", "), nil
				}},
				{"privilèges du token", false, func(ctx context.Context, c *pve.Client) (string, error) {
					perms, err := c.Permissions(ctx, "")
					if err != nil {
						return "", err
					}
					return summarisePermissions(perms), nil
				}},
			}

			var failed error
			for _, ch := range checks {
				if failed != nil {
					_, _ = fmt.Fprintf(out, "·  %s — non exécuté\n", ch.label)
					continue
				}
				detail, err := ch.run(cmd.Context(), client)
				if err != nil {
					_, _ = fmt.Fprintf(out, "✗  %s\n", ch.label)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%v\n\n", err)
					if ch.blocking {
						failed = err
					}
					continue
				}
				_, _ = fmt.Fprintf(out, "✓  %s — %s\n", ch.label, detail)
			}

			for _, w := range warnings(eff.TokenID, eff.Endpoint, client.TrustMode(), hasAccessToken) {
				_, _ = fmt.Fprintf(out, "⚠  %s\n", w)
			}

			return failed
		},
	}
}

// warnings flags the two habits this project exists to avoid, plus the one
// misdiagnosis a node published on the internet invites.
func warnings(tokenID, endpoint string, mode pve.TrustMode, hasAccessToken bool) []string {
	var out []string
	if mode == pve.TrustNone {
		out = append(out, "vérification TLS désactivée — épingle l'empreinte : pvecli config trust")
	}
	if strings.HasPrefix(tokenID, "root@pam") {
		out = append(out, "token porté par root@pam — utilise une identité dédiée avec le rôle minimal")
	}
	if !hasAccessToken && isPublicHost(endpoint) {
		out = append(out, "endpoint public sans service token Cloudflare — si un 403 tombe ici,\n"+
			"   il vient peut-être d'Access et non de Proxmox : exporte CF_ACCESS_CLIENT_ID\n"+
			"   et CF_ACCESS_CLIENT_SECRET")
	}
	return out
}

// localTLDs are the suffixes a homelab gives itself. A node behind one of them
// is reached directly, whatever the DNS says.
var localTLDs = []string{".lan", ".local", ".home", ".internal", ".localdomain"}

// isPublicHost reports whether the endpoint looks like a name published on the
// internet — the only case where an Access application can sit in the way.
// A private address, a loopback, a bare hostname or a homelab suffix is not one.
func isPublicHost(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, suffix := range localTLDs {
		if strings.HasSuffix(strings.ToLower(host), suffix) {
			return false
		}
	}
	return true
}

// summarisePermissions turns the permission map into the one sentence that
// matters: read-only, or able to write.
func summarisePermissions(perms map[string]map[string]int) string {
	paths := make([]string, 0, len(perms))
	writes := 0
	for path, privs := range perms {
		paths = append(paths, path)
		for name, granted := range privs {
			if granted == 1 && !strings.HasSuffix(name, ".Audit") {
				writes++
			}
		}
	}
	sort.Strings(paths)

	scope := "aucun chemin"
	if len(paths) > 0 {
		scope = fmt.Sprintf("%d chemin(s), dont %s", len(paths), paths[0])
	}
	if writes == 0 {
		return scope + " — lecture seule (aucun privilège hors *.Audit)"
	}
	return fmt.Sprintf("%s — %d privilège(s) d'écriture", scope, writes)
}
