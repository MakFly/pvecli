package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MakFly/pvecli/internal/cf"
	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/secret"
)

// cfAccessSecretRef is where a service token's client secret is filed. Same
// service as the tunnel token, different account prefix: one trousseau entry
// per credential, none of them ever printed twice.
func cfAccessSecretRef(name string) secret.Ref {
	return secret.Ref{Service: "pvecli-cloudflare", Account: "access-" + name}
}

func newCFAccessCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "access",
		Short: "Pose la porte devant un tunnel",
		Long: `Gère les applications Cloudflare Access, leurs policies et leurs service tokens.

Un tunnel rend un service ATTEIGNABLE. Access décide QUI l'atteint. Les deux
sont indépendants : publier un nom sans application Access devant, c'est poser
l'interface Proxmox sur l'internet ouvert.

L'ordre qui évite ça :

  pvecli cf access app create pve.exemple.tld --name "Proxmox lab"
  pvecli cf access policy add --app pve.exemple.tld --name humains --email moi@…
  pvecli cf route add pve.exemple.tld --tunnel lab --service https://…:8006 --no-tls-verify

Un navigateur s'authentifie ; une CLI, non. Pour qu'un client qui n'est pas un
navigateur passe, il lui faut un service token ET une policy en « service auth » :

  pvecli cf access token create pvecli-collegue
  pvecli cf access policy add --app pve.exemple.tld --name cli --service-token pvecli-collegue`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newCFAccessAppCmd(), newCFAccessPolicyCmd(), newCFAccessTokenCmd())
	return c
}

// ------------------------------------------------------------------ apps

func newCFAccessAppCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "app",
		Short: "Les hostnames protégés par Access",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newCFAccessAppCreateCmd(), newCFAccessAppListCmd(), newCFAccessAppRemoveCmd())
	return c
}

func newCFAccessAppCreateCmd() *cobra.Command {
	var name, session string

	c := &cobra.Command{
		Use:   "create <fqdn>",
		Short: "Met une application Access devant un nom",
		Long: `Crée une application « self-hosted » : un nom public dont Access garde l'entrée.

Une application FRAÎCHEMENT CRÉÉE n'admet personne. C'est l'état correct — elle
ferme avant de savoir à qui ouvrir. Les policies viennent ensuite.

Endpoint : POST /accounts/{account}/access/apps`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			errW := cmd.ErrOrStderr()

			// PRE-READ: two applications on one hostname is a state whose
			// effective policy nobody can predict from the outside.
			if existing, err := client.AppByDomain(ctx, domain); err == nil {
				return fmt.Errorf("une application Access couvre déjà « %s » (%s)", domain, existing.ID)
			}

			if name == "" {
				name = domain
			}
			app := cf.App{Name: name, Domain: domain, SessionDuration: session}

			_, _ = fmt.Fprintf(errW, "  requête  POST /accounts/%s/access/apps\n", client.AccountID())
			_, _ = fmt.Fprintf(errW, "  payload\n    name             %s\n    domain           %s\n"+
				"    type             %s\n    session_duration %s\n", name, domain, cf.AppTypeSelfHosted, session)
			_, _ = fmt.Fprintf(errW, "  effet    « %s » fermé à tout le monde, en attente d'une policy\n", domain)

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été créé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(domain, false); err != nil {
				return err
			}

			if _, err := client.CreateApp(ctx, app); err != nil {
				return err
			}

			// POST-READ: re-read rather than echo what was sent.
			back, err := client.AppByDomain(ctx, domain)
			if err != nil {
				return fmt.Errorf("application créée mais introuvable à la relecture : %w", err)
			}

			_, _ = fmt.Fprintf(errW, "\n« %s » est fermé à tout le monde. Ouvre-le à quelqu'un :\n"+
				"  pvecli cf access policy add --app %s --name humains --email <adresse>\n", domain, domain)

			return renderApps(cmd, []cf.App{back})
		},
	}
	c.Flags().StringVar(&name, "name", "", "nom affiché dans le dashboard (défaut : le fqdn)")
	c.Flags().StringVar(&session, "session", "24h", "durée de session, ex. 24h ou 30m")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newCFAccessAppListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les applications Access",
		Args:    usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			apps, err := client.Apps(cmd.Context())
			if err != nil {
				return err
			}
			return renderApps(cmd, apps)
		},
	}
	addRenderFlags(c)
	return c
}

func newCFAccessAppRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "rm <fqdn>",
		Short: "Retire l'application, et la protection avec",
		Long: `Supprime l'application Access qui couvre un nom.

Le tunnel, lui, continue de router. Retirer l'application sans retirer la route
laisse le service EXPOSÉ — c'est l'ordre inverse de la mise en place, et il est
volontairement bruyant.

Endpoint : DELETE /accounts/{account}/access/apps/{app}`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			errW := cmd.ErrOrStderr()

			app, err := client.AppByDomain(ctx, domain)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(errW, "  requête  DELETE /accounts/%s/access/apps/%s\n", client.AccountID(), app.ID)
			_, _ = fmt.Fprintf(errW, "  effet    « %s » n'est plus protégé par Access\n", domain)
			_, _ = fmt.Fprintf(errW, "  ⚠ si une route de tunnel pointe encore ce nom, il devient PUBLIC.\n"+
				"    Retire-la d'abord :  pvecli cf route rm %s --tunnel <tunnel>\n", domain)

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été supprimé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			// Destructive: it removes a protection. Retyping the name is the point.
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(domain, true); err != nil {
				return err
			}

			if err := client.DeleteApp(ctx, app.ID); err != nil {
				return err
			}
			if _, err := client.AppByDomain(ctx, domain); err == nil {
				return fmt.Errorf("l'application pour %s est toujours là après suppression", domain)
			}
			_, _ = fmt.Fprintf(errW, "\napplication supprimée : %s\n", domain)
			return nil
		},
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func renderApps(cmd *cobra.Command, apps []cf.App) error {
	rows := output.Rows{Headers: []string{"NOM PUBLIC", "NOM", "SESSION", "IDENTIFIANT"}}
	for _, a := range apps {
		rows.Cells = append(rows.Cells, []string{a.Domain, a.Name, a.SessionDuration, a.ID})
	}
	opts, err := renderOptions(cmd)
	if err != nil {
		return err
	}
	return output.Render(cmd.OutOrStdout(), opts, apps, rows)
}

// -------------------------------------------------------------- policies

func newCFAccessPolicyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "policy",
		Short: "Qui passe la porte",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newCFAccessPolicyAddCmd(), newCFAccessPolicyListCmd(), newCFAccessPolicyRemoveCmd())
	return c
}

func newCFAccessPolicyAddCmd() *cobra.Command {
	var appDomain, name, serviceToken string
	var emails, emailDomains []string

	c := &cobra.Command{
		Use:   "add",
		Short: "Ajoute une règle d'admission à une application",
		Long: `Attache une policy à une application Access.

Les personnes passent par leur adresse, et reçoivent un code à usage unique :

  --email moi@exemple.tld --email collegue@exemple.tld
  --email-domain exemple.tld            (tout le monde chez ce domaine)

Une CLI ne peut pas s'authentifier comme une personne. Elle présente un service
token, et cela exige une policy « service auth » — décision « non_identity ».
pvecli la pose automatiquement avec --service-token, et REFUSE de mettre un
service token dans une policy « allow » : ça laisserait passer sans aucune
authentification, ce qui est l'inverse de l'intention.

Endpoint : POST /accounts/{account}/access/apps/{app}/policies`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if appDomain == "" {
				return &exitError{code: pve.ExitUsage, msg: "--app est obligatoire, ex. --app pve.exemple.tld"}
			}
			if len(emails) == 0 && len(emailDomains) == 0 && serviceToken == "" {
				return &exitError{code: pve.ExitUsage,
					msg: "il faut dire QUI passe : --email, --email-domain ou --service-token.\n" +
						"Une policy sans include n'admet personne."}
			}
			// Mixing a service token with people in one policy cannot work: the
			// decision would have to be two things at once.
			if serviceToken != "" && (len(emails) > 0 || len(emailDomains) > 0) {
				return &exitError{code: pve.ExitUsage,
					msg: "un service token et des personnes ne tiennent pas dans la même policy :\n" +
						"les premières s'authentifient (« allow »), le second non (« non_identity »).\n" +
						"Fais-en deux."}
			}

			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			errW := cmd.ErrOrStderr()

			app, err := client.AppByDomain(ctx, appDomain)
			if err != nil {
				return err
			}

			policy := cf.Policy{Name: name, Decision: cf.DecisionAllow}
			if serviceToken != "" {
				token, err := client.ServiceTokenByName(ctx, serviceToken)
				if err != nil {
					return err
				}
				policy.Decision = cf.DecisionServiceAuth
				policy.Include = append(policy.Include, cf.IncludeServiceToken(token.ID))
			}
			for _, e := range emails {
				policy.Include = append(policy.Include, cf.IncludeEmail(e))
			}
			for _, d := range emailDomains {
				policy.Include = append(policy.Include, cf.IncludeEmailDomain(d))
			}
			if policy.Name == "" {
				policy.Name = policy.Describe()
			}
			if err := policy.Validate(); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(errW, "  application  %s (%s)\n", app.Domain, app.ID)
			_, _ = fmt.Fprintf(errW, "  décision     %s\n", policy.Decision)
			_, _ = fmt.Fprintf(errW, "  admet        %s\n", policy.Describe())

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été écrit.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(app.Domain, false); err != nil {
				return err
			}

			if _, err := client.AddPolicy(ctx, app.ID, policy); err != nil {
				return err
			}

			// POST-READ: the policies as Cloudflare now holds them.
			back, err := client.Policies(ctx, app.ID)
			if err != nil {
				return err
			}
			return renderPolicies(cmd, back)
		},
	}
	f := c.Flags()
	f.StringVar(&appDomain, "app", "", "nom public de l'application, ex. pve.exemple.tld")
	f.StringVar(&name, "name", "", "nom de la policy (défaut : ce qu'elle admet)")
	f.StringSliceVar(&emails, "email", nil, "adresse admise — répétable")
	f.StringSliceVar(&emailDomains, "email-domain", nil, "domaine d'adresses admis — répétable")
	f.StringVar(&serviceToken, "service-token", "", "nom d'un service token — pose une policy « service auth »")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newCFAccessPolicyListCmd() *cobra.Command {
	var appDomain string

	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les policies d'une application",
		Args:    usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if appDomain == "" {
				return &exitError{code: pve.ExitUsage, msg: "--app est obligatoire"}
			}
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			app, err := client.AppByDomain(cmd.Context(), appDomain)
			if err != nil {
				return err
			}
			policies, err := client.Policies(cmd.Context(), app.ID)
			if err != nil {
				return err
			}
			if len(policies) == 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"⚠ « %s » n'a aucune policy : Access refuse tout le monde.\n", app.Domain)
			}
			return renderPolicies(cmd, policies)
		},
	}
	c.Flags().StringVar(&appDomain, "app", "", "nom public de l'application")
	addRenderFlags(c)
	return c
}

func newCFAccessPolicyRemoveCmd() *cobra.Command {
	var appDomain string

	c := &cobra.Command{
		Use:   "rm <identifiant>",
		Short: "Retire une policy",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			policyID := args[0]
			if appDomain == "" {
				return &exitError{code: pve.ExitUsage, msg: "--app est obligatoire"}
			}
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			app, err := client.AppByDomain(ctx, appDomain)
			if err != nil {
				return err
			}

			errW := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(errW, "  application  %s (%s)\n  policy       %s\n", app.Domain, app.ID, policyID)

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été supprimé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(policyID, false); err != nil {
				return err
			}

			if err := client.DeletePolicy(ctx, app.ID, policyID); err != nil {
				return err
			}
			back, err := client.Policies(ctx, app.ID)
			if err != nil {
				return err
			}
			if len(back) == 0 {
				_, _ = fmt.Fprintf(errW, "\n⚠ « %s » n'a plus aucune policy : Access refuse désormais tout le monde.\n", app.Domain)
			}
			return renderPolicies(cmd, back)
		},
	}
	c.Flags().StringVar(&appDomain, "app", "", "nom public de l'application")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func renderPolicies(cmd *cobra.Command, policies []cf.Policy) error {
	rows := output.Rows{Headers: []string{"NOM", "DÉCISION", "ADMET", "IDENTIFIANT"}}
	for _, p := range policies {
		decision := p.Decision
		if p.Decision == cf.DecisionServiceAuth {
			decision += " (service auth)"
		}
		rows.Cells = append(rows.Cells, []string{p.Name, decision, p.Describe(), p.ID})
	}
	opts, err := renderOptions(cmd)
	if err != nil {
		return err
	}
	return output.Render(cmd.OutOrStdout(), opts, policies, rows)
}

// --------------------------------------------------------- service tokens

func newCFAccessTokenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "token",
		Short: "Les identifiants des clients qui ne sont pas des navigateurs",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newCFAccessTokenCreateCmd(), newCFAccessTokenListCmd())
	return c
}

func newCFAccessTokenCreateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "create <nom>",
		Short: "Crée un service token et range son secret",
		Long: `Crée un service token : de quoi faire passer une CLI par une porte Access.

LE SECRET N'EST RENDU QU'UNE FOIS. Il part au trousseau, sous
« pvecli-cloudflare / access-<nom> », et n'est jamais affiché. Cloudflare ne le
conserve pas sous une forme relisible.

Le token seul ne donne accès à rien : il faut encore une policy qui l'admette.

  pvecli cf access policy add --app <fqdn> --name cli --service-token <nom>

Endpoint : POST /accounts/{account}/access/service_tokens`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			errW := cmd.ErrOrStderr()

			if existing, err := client.ServiceTokenByName(ctx, name); err == nil {
				return fmt.Errorf("un service token « %s » existe déjà (%s) — "+
					"toute commande qui résout par le nom deviendrait ambiguë", name, existing.ID)
			}

			_, _ = fmt.Fprintf(errW, "  requête  POST /accounts/%s/access/service_tokens\n", client.AccountID())
			_, _ = fmt.Fprintf(errW, "  payload\n    name             %s\n", name)
			_, _ = fmt.Fprintf(errW, "  effet    un identifiant utilisable, admis par aucune policy\n")

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été créé.")
				return nil
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if err := (cliGate{cmd: cmd, yes: yes}).Allow(name, false); err != nil {
				return err
			}

			token, err := client.CreateServiceToken(ctx, name)
			if err != nil {
				return err
			}

			// The secret is handled BEFORE anything else can fail: it comes back
			// exactly once, and an error after this point would leave a live
			// credential nobody holds.
			stored := "non conservé"
			if token.ClientSecret != "" {
				if secretAvailable() {
					if err := secretStore(cfAccessSecretRef(name), token.ClientSecret); err != nil {
						return err
					}
					stored = cfAccessSecretRef(name).String()
				} else {
					_, _ = fmt.Fprintf(errW,
						"\n⚠ aucun trousseau sur cette plateforme. Secret, affiché UNE fois :\n  %s\n",
						token.ClientSecret)
				}
			}

			_, _ = fmt.Fprintf(errW, "\nCe token ne donne encore accès à rien. Admets-le quelque part :\n"+
				"  pvecli cf access policy add --app <fqdn> --name cli --service-token %s\n\n"+
				"Puis, sur la machine cliente :\n"+
				"  export CF_ACCESS_CLIENT_ID=\"%s\"\n"+
				"  export CF_ACCESS_CLIENT_SECRET=\"$(security find-generic-password -s pvecli-cloudflare -a access-%s -w)\"\n",
				name, token.ClientID, name)

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"nom", name},
				{"identifiant", token.ID},
				{"client_id", token.ClientID},
				{"secret", stored},
			}}
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			// The secret is stripped from the structured output too: `-o json`
			// lands in files and pipelines more often than a terminal does.
			token.ClientSecret = ""
			return output.Render(cmd.OutOrStdout(), opts, token, rows)
		},
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newCFAccessTokenListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les service tokens du compte",
		Args:    usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newCFClient(cmd)
			if err != nil {
				return err
			}
			tokens, err := client.ServiceTokens(cmd.Context())
			if err != nil {
				return err
			}
			rows := output.Rows{Headers: []string{"NOM", "CLIENT_ID", "IDENTIFIANT"}}
			for _, t := range tokens {
				rows.Cells = append(rows.Cells, []string{t.Name, t.ClientID, t.ID})
			}
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts, tokens, rows)
		},
	}
	addRenderFlags(c)
	return c
}

// accessCoverage says whether a hostname is protected, in one line, for the
// command that publishes it. Any error is reported as "unknown" rather than
// swallowed: claiming a name is covered when the check itself failed is the one
// answer that must never be given.
func accessCoverage(cmd *cobra.Command, client *cf.Client, fqdn string) string {
	app, err := client.AppByDomain(cmd.Context(), fqdn)
	if err != nil {
		if strings.Contains(err.Error(), cf.ErrNotFound.Error()) {
			return ""
		}
		return "inconnue (" + err.Error() + ")"
	}
	policies, err := client.Policies(cmd.Context(), app.ID)
	if err != nil {
		return "inconnue (" + err.Error() + ")"
	}
	if len(policies) == 0 {
		return "application présente, AUCUNE policy — Access refuse tout le monde"
	}
	names := make([]string, 0, len(policies))
	for _, p := range policies {
		names = append(names, p.Describe())
	}
	return strings.Join(names, " · ")
}
