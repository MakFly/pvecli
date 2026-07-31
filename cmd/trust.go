package cmd

import (
	"fmt"
	"strings"

	"github.com/MakFly/pvectl/internal/pve"
	"github.com/spf13/cobra"
)

func newConfigTrustCmd() *cobra.Command {
	var yes bool

	c := &cobra.Command{
		Use:   "trust",
		Short: "Récupère l'empreinte du certificat du nœud et l'épingle",
		Long: `Ouvre une connexion TLS vers l'endpoint configuré, affiche le certificat
présenté, et enregistre son empreinte SHA-256 après confirmation.

Une fois l'empreinte épinglée, la vérification est réelle et n'a besoin
d'aucune autorité de certification : ce certificat précis, ou aucun. C'est
strictement plus fort que --insecure, et ça coûte cette commande, une fois.

--yes court-circuite la confirmation pour un premier épinglage. Il ne la
court-circuite PAS si une empreinte différente est déjà enregistrée : un
certificat qui change est un incident, pas une routine de script.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			if eff.Endpoint == "" {
				return fmt.Errorf("aucun endpoint configuré — lance « pvectl config init --endpoint https://…:8006 »")
			}

			cert, err := pve.FetchCertificate(cmd.Context(), eff.Endpoint)
			if err != nil {
				return err
			}
			got := pve.Fingerprint(cert)

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "endpoint    %s\n", eff.Endpoint)
			_, _ = fmt.Fprintf(out, "sujet       %s\n", cert.Subject)
			_, _ = fmt.Fprintf(out, "émetteur    %s\n", cert.Issuer)
			_, _ = fmt.Fprintf(out, "valide      du %s au %s\n",
				cert.NotBefore.Format("2006-01-02"), cert.NotAfter.Format("2006-01-02"))
			_, _ = fmt.Fprintf(out, "empreinte   %s\n", got)

			stderr := cmd.ErrOrStderr()
			current := eff.TLS.Fingerprint

			switch {
			case current != "" && strings.EqualFold(normalize(current), normalize(got)):
				_, _ = fmt.Fprintln(stderr, "\nempreinte déjà épinglée et inchangée — rien à faire.")
				return nil

			case current != "":
				// A pinned certificate that changed is exactly what pinning
				// exists to catch. Never auto-accept it.
				_, _ = fmt.Fprintf(stderr, `
⚠  UNE EMPREINTE DIFFÉRENTE EST DÉJÀ ÉPINGLÉE.

   enregistrée : %s
   présentée   : %s

   Soit le nœud a été réinstallé ou son certificat régénéré, soit quelqu'un
   s'est intercalé. Vérifie depuis la console du nœud avant de répondre :

     openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -fingerprint -sha256

`, current, got)
				if err := confirm(cmd, "Remplacer l'empreinte enregistrée ?"); err != nil {
					return err
				}

			default:
				if !yes {
					if err := confirm(cmd, "\nÉpingler cette empreinte ?"); err != nil {
						return err
					}
				}
			}

			name, err := writeKey(cmd, "tls.fingerprint", got)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stderr, "empreinte épinglée dans le contexte « %s ».\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "épingle sans confirmation (premier épinglage seulement)")
	return c
}

// normalize strips the separators so two spellings of the same fingerprint
// compare equal.
func normalize(fp string) string {
	return strings.ToUpper(strings.NewReplacer(":", "", " ", "", "-", "").Replace(fp))
}
