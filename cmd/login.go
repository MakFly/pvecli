package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/MakFly/pvecli/internal/config"
	"github.com/MakFly/pvecli/internal/log"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// EnvLoginPassword is where a non-interactive login reads the password from.
// A flag would be visible in `ps` and would stay in the shell history; the
// environment is the least bad of the three, and the prompt is better still.
const EnvLoginPassword = "PVE_PASSWORD"

// Defaults of the identity `login` creates. They match what the README, the
// PRD and the error messages of the whole CLI already name, so that the
// bootstrap produces exactly the token the rest of the documentation expects.
const (
	defaultBootstrapUser  = "automation@pve"
	defaultBootstrapToken = "pvectl"
	defaultBootstrapRole  = "PVEAdmin"
	defaultBootstrapPath  = "/"
)

// newLoginCmd bootstraps an API token from a password.
//
// Pourquoi cette commande existe : jusqu'ici, pvecli savait tout faire SAUF
// obtenir de quoi le faire. Sur une machine neuve il fallait ouvrir un SSH vers
// le nœud et y taper trois `pveum` — c'est-à-dire que l'outil censé administrer
// le nœud ne franchissait pas sa propre porte d'entrée, et qu'il exigeait
// justement l'accès shell qu'une API est là pour éviter.
func newLoginCmd() *cobra.Command {
	var (
		user      string
		userID    string
		tokenID   string
		role      string
		aclPath   string
		writeConf bool
	)

	c := &cobra.Command{
		Use:   "login",
		Short: "Ouvre une session par mot de passe et fabrique le token d'API",
		Long: `Échange un mot de passe contre un token d'API, une fois.

C'est l'amorçage : toutes les autres commandes s'authentifient par token, et
aucune ne savait en créer un. Sans cette commande, il fallait un accès SSH au
nœud pour lancer « pveum » — soit exactement l'accès que l'API doit rendre
inutile.

Le mot de passe ne sert qu'à obtenir un ticket (POST /access/ticket, ce que fait
l'interface web quand tu t'y connectes). Avec ce ticket, la commande :

  1. crée l'utilisateur ` + defaultBootstrapUser + ` s'il n'existe pas (sans mot de passe :
     cette identité ne porte que des tokens) ;
  2. crée le token ` + defaultBootstrapToken + ` et lit son secret — PVE ne le montre QU'UNE FOIS ;
  3. lui attache le rôle ` + defaultBootstrapRole + ` sur ` + defaultBootstrapPath + `, en propagation ;
  4. écrit le token_id dans la configuration.

Le secret, lui, n'est jamais écrit sur le disque. Il est imprimé une fois, à toi
de l'exporter ou de le ranger. C'est la même règle que partout ailleurs dans
cette CLI : le fichier de configuration ne contient jamais de secret.

Le mot de passe se saisit au clavier, sans écho. En script :

  PVE_PASSWORD=… pvecli login --user root@pam

Rejouable : si l'utilisateur ou le token existent déjà, ils sont laissés en
place et l'ACL est réappliquée. Un token existant ne peut pas rendre son secret
une seconde fois — le nœud ne le stocke pas en clair. Pour en repartir :

  pvecli access token delete ` + defaultBootstrapUser + ` ` + defaultBootstrapToken + `

Endpoints : POST /access/ticket, POST /access/users, POST /access/users/{id}/token/{t}, PUT /access/acl`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			if eff.Endpoint == "" {
				return fmt.Errorf("aucun endpoint — lance « pvecli config init --endpoint https://…:8006 »")
			}

			password, err := readLoginPassword(cmd)
			if err != nil {
				return err
			}

			timeout, _ := cmd.Flags().GetDuration("timeout")
			verbosity, _ := cmd.Flags().GetCount("verbose")
			accessID := os.Getenv(config.EnvAccessClientID)
			accessSecret := os.Getenv(config.EnvAccessClientSecret)
			// Le mot de passe est confié au traceur pour qu'il le masque partout
			// où il pourrait ressortir : un corps de requête, une URL de
			// redirection, un message d'erreur renvoyé par le nœud.
			tracer := log.New(cmd.ErrOrStderr(), log.LevelFor(verbosity), password, accessSecret)

			opts := pve.Options{
				Endpoint:           eff.Endpoint,
				Timeout:            timeout,
				AccessClientID:     accessID,
				AccessClientSecret: accessSecret,
				Trace:              traceOrNil(tracer),
				Trust: pve.TrustOptions{
					Fingerprint: eff.TLS.Fingerprint,
					CAFile:      eff.TLS.CAFile,
					Insecure:    eff.Insecure,
				},
			}

			out := cmd.OutOrStdout()
			ticket, client, err := pve.Login(cmd.Context(), opts, user, password)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "session ouverte pour %s\n", ticket.Username)

			res, bootErr := client.EnsureAutomationToken(cmd.Context(), pve.Bootstrap{
				UserID:  userID,
				TokenID: tokenID,
				Role:    role,
				Path:    aclPath,
				Comment: "pvecli — amorçage par « pvecli login »",
			})

			// Le secret d'abord, l'erreur ensuite. L'ordre n'est pas cosmétique :
			// si le token a été créé puis que la pose de l'ACL a échoué, le nœud
			// ne remontrera JAMAIS ce secret. Sortir sur l'erreur sans l'avoir
			// imprimé, ce serait laisser derrière soi un token inutilisable
			// qu'il faut aller supprimer à la main.
			if res != nil && res.Secret != "" {
				_, _ = fmt.Fprintf(out, `
Le secret n'est montré QU'UNE FOIS — le nœud ne le garde pas en clair.

    export PVE_API_TOKEN_ID='%s'
    export PVE_API_TOKEN_SECRET='%s'

`, res.FullTokenID, res.Secret)
			}
			if bootErr != nil {
				if res != nil && res.TokenCreated {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"Le token a bien été créé (secret ci-dessus) mais l'amorçage n'est pas\n"+
							"allé au bout — il lui manque probablement son ACL. Reprends avec :\n"+
							"    pvecli access acl set %s --role %s --token %s\n\n",
						aclPath, role, res.FullTokenID)
				}
				return bootErr
			}

			_, _ = fmt.Fprintf(out, "utilisateur  %-28s %s\n", res.FullTokenID[:strings.Index(res.FullTokenID, "!")],
				etatCree(res.UserCreated))
			_, _ = fmt.Fprintf(out, "token        %-28s %s\n", res.FullTokenID, etatCree(res.TokenCreated))
			_, _ = fmt.Fprintf(out, "ACL          %-28s posée\n", role+" sur "+aclPath)

			if writeConf {
				ctxName, err := writeKey(cmd, "token_id", res.FullTokenID)
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"note : token_id non écrit dans la configuration (%v)\n", err)
				} else {
					_, _ = fmt.Fprintf(out, "config       token_id enregistré dans le contexte « %s »\n", ctxName)
				}
			}

			if res.Secret == "" {
				_, _ = fmt.Fprintf(out, `
Le token existait déjà, et son secret ne peut pas être relu : le nœud ne le
conserve pas en clair. Si tu ne l'as plus, révoque-le et relance :

    pvecli access token delete %s %s
    pvecli login --user %s
`, userID, tokenID, user)
				return nil
			}

			_, _ = fmt.Fprintf(out, "Puis vérifie la chaîne complète :\n\n    pvecli doctor\n")
			return nil
		},
	}

	f := c.Flags()
	f.StringVar(&user, "user", "root@pam", "compte pour la connexion, royaume compris")
	f.StringVar(&userID, "token-user", defaultBootstrapUser, "identité qui portera le token")
	f.StringVar(&tokenID, "token-name", defaultBootstrapToken, "nom du token à créer")
	f.StringVar(&role, "role", defaultBootstrapRole, "rôle attaché au token")
	f.StringVar(&aclPath, "path", defaultBootstrapPath, "chemin sur lequel l'ACL est posée")
	f.BoolVar(&writeConf, "write-config", true, "enregistrer le token_id dans la configuration")
	return c
}

func etatCree(created bool) string {
	if created {
		return "créé"
	}
	return "existait déjà"
}

// readLoginPassword takes the password from the environment, or asks for it on
// the terminal without echo.
//
// No --password flag, and that is deliberate: a flag shows up in `ps` for every
// user of the machine and stays in the shell history. The prompt leaves no
// trace at all; the environment variable is the compromise for scripts.
func readLoginPassword(cmd *cobra.Command) (string, error) {
	if v := os.Getenv(EnvLoginPassword); v != "" {
		return v, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Le type maison plutôt qu'un fmt.Errorf multiligne : il porte le code
		// de sortie « auth » que les scripts lisent, et sépare proprement la
		// cause du conseil.
		return "", &pve.AuthError{
			Reason: "aucun mot de passe : " + EnvLoginPassword +
				" n'est pas défini et l'entrée standard n'est pas un terminal",
			Hint: "  " + EnvLoginPassword + "='…' pvecli login --user root@pam\n\n" +
				"Il n'y a pas de --password : un flag est visible dans « ps » et reste\n" +
				"dans l'historique du shell.",
		}
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "mot de passe : ")
	raw, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("lecture du mot de passe : %w", err)
	}
	return string(raw), nil
}
