package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/service"
)

func newAccessCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "access",
		Short: "Identités, rôles, ACL et tokens",
		Long: `Lit et modifie le modèle d'autorisation de PVE.

Trois objets qu'on confond tant qu'on ne les a pas séparés :

  privilège   un atome — VM.PowerMgmt, Datastore.Audit
  rôle        un paquet nommé de privilèges — PVEVMAdmin, PVEAuditor
  ACL         un triplet (chemin, identité, rôle), qui propage ou non

Les droits effectifs se lisent sur un CHEMIN, jamais sur un objet. Tant que ce
modèle n'est pas clair, chaque 403 reste une énigme.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newWhoamiCmd(), newUserCmd(), newRoleCmd(), newACLCmd(), newTokenCmd())
	return c
}

// ---------------------------------------------------------------- whoami

func newWhoamiCmd() *cobra.Command {
	var (
		path string
		can  string
	)

	c := &cobra.Command{
		Use:   "whoami",
		Short: "Droits effectifs du token courant (GET /access/permissions)",
		Long: `Affiche ce que le token courant a réellement le droit de faire, chemin par
chemin, du plus général au plus spécifique.

C'est la commande vers laquelle pointe le message d'erreur d'un 403 : changer
de token ne corrige rien, c'est une ACL qu'il faut lire puis corriger.

  --path /vms/120                       ne garde que ce chemin
  --can VM.PowerMgmt --path /vms/120    répond oui/non, et sort en 0 ou 1

PRIVILEGE SEPARATION. Un token créé avec privsep=1 n'a que les droits de ses
PROPRES ACL, intersectés avec ceux de son utilisateur. Il peut donc avoir moins
de droits que l'utilisateur qui le porte, jamais plus. C'est la cause n°1 des
403 inexplicables : l'ACL a été posée sur l'utilisateur, et pas sur le token.

Endpoint : GET /api2/json/access/permissions`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			// The path is handed to the API, never applied to the full dump:
			// the dump lists only the paths carrying an ACL, while asking for
			// one path makes the node RESOLVE inheritance. Filtering here would
			// answer "aucun privilège" on /vms/120 whose rights come from a
			// propagated ACL on /vms.
			perms, err := client.Permissions(cmd.Context(), path)
			if err != nil {
				return err
			}

			if can != "" {
				return answerCan(cmd, perms, path, can)
			}

			rows := output.Rows{Headers: []string{"CHEMIN", "PRIVILÈGES"}}
			for _, p := range sortedPaths(perms) {
				rows.Cells = append(rows.Cells, []string{p, strings.Join(grantedPrivileges(perms[p]), ", ")})
			}

			if len(rows.Cells) == 0 && path != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"aucun privilège sur « %s » — vérifie la propagation de l'ACL parente :\n  pvecli access acl ls\n", path)
			}

			describeTokenSeparation(cmd, client)
			return output.Render(cmd.OutOrStdout(), opts, perms, rows)
		},
	}

	c.Flags().StringVar(&path, "path", "", "ne montre que ce chemin ACL")
	c.Flags().StringVar(&can, "can", "", "répond oui/non pour ce privilège (avec --path)")
	addRenderFlags(c)
	return c
}

// answerCan is the scriptable half of whoami: one word on stdout, and an exit
// code a shell can branch on.
func answerCan(cmd *cobra.Command, perms map[string]map[string]int, path, priv string) error {
	if path == "" {
		return &exitError{code: pve.ExitUsage, msg: "--can attend aussi --path : un privilège se lit sur un chemin"}
	}

	if perms[path][priv] == 1 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "oui")
		return nil
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "non")

	// Exit code 1, and a line saying which ACL would fix it. A bare "non" is
	// scriptable but teaches nothing.
	return &exitError{
		code: 1,
		msg: fmt.Sprintf("privilège %s absent sur %s — pose l'ACL qui le donne :\n"+
			"  pvecli access role ls | grep %s\n"+
			"  pvecli access acl set --path %s --role <rôle> --token <token>",
			priv, path, strings.SplitN(priv, ".", 2)[0], path),
	}
}

// describeTokenSeparation says whether the current identity is a token, and
// whether it is privilege-separated. Silent when the answer cannot be read:
// this is context, not the command's result.
func describeTokenSeparation(cmd *cobra.Command, client *pve.Client) {
	full := client.TokenID()
	user, tokenID, ok := pve.SplitTokenID(full)
	if !ok {
		return
	}

	info, err := client.TokenInfo(cmd.Context(), user, tokenID)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "identité : token « %s » de %s\n", tokenID, user)
		return
	}
	if info.Separated() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"identité : token « %s » de %s, privsep=1 — les droits effectifs sont\n"+
				"l'INTERSECTION de ceux du token et de ceux de %s. Une ACL posée sur le\n"+
				"seul utilisateur ne donne rien au token, et réciproquement.\n",
			tokenID, user, user)
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"identité : token « %s » de %s, privsep=0 — il porte TOUS les droits de %s.\n",
		tokenID, user, user)
}

// sortedPaths orders ACL paths from the most general to the most specific, so
// the listing reads the way privileges are inherited.
func sortedPaths(perms map[string]map[string]int) []string {
	paths := make([]string, 0, len(perms))
	for p := range perms {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		di, dj := strings.Count(strings.Trim(paths[i], "/"), "/"), strings.Count(strings.Trim(paths[j], "/"), "/")
		if paths[i] == "/" {
			return true
		}
		if paths[j] == "/" {
			return false
		}
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
	return paths
}

func grantedPrivileges(privs map[string]int) []string {
	out := make([]string, 0, len(privs))
	for name, granted := range privs {
		if granted == 1 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- users

func newUserCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "user",
		Short: "Comptes déclarés sur le nœud",
		Args:  usage(cobra.NoArgs),
	}

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les utilisateurs (GET /access/users)",
		Args:    usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			users, err := client.Users(cmd.Context())
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"UTILISATEUR", "ACTIF", "EXPIRE", "COMMENTAIRE"}}
			for _, u := range users {
				rows.Cells = append(rows.Cells, []string{
					u.UserID, yesNo(u.Enable == 1), expiryLabel(u.Expire), u.Comment,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, users, rows)
		},
	}
	addRenderFlags(ls)
	c.AddCommand(ls, newUserCreateCmd())
	return c
}

// EnvNewUserPassword is where the initial password of a new account comes from.
// Same rule as the API token secret: the environment, never a flag — a flag is
// visible in `ps` to every user of the machine, and stays in the shell history.
const EnvNewUserPassword = "PVE_NEW_USER_PASSWORD"

func newUserCreateCmd() *cobra.Command {
	var comment, email, groups, expire string
	var noExpire, disable, noPassword bool

	c := &cobra.Command{
		Use:   "create <utilisateur@realm>",
		Short: "Crée un compte (POST /access/users)",
		Long: `Crée un compte sur le nœud.

L'identifiant porte TOUJOURS son realm : « collegue@pve », jamais « collegue ».
Le realm fait partie de l'identité — deux comptes du même nom dans deux realms
sont deux personnes différentes.

Le mot de passe initial ne se donne pas en argument. Il vient de
` + EnvNewUserPassword + `, ou d'une saisie masquée si le terminal le permet :

  export PVE_NEW_USER_PASSWORD="…"        # 8 caractères minimum, exigence du nœud
  pvecli access user create collegue@pve --comment "accès lab"

--no-password crée un compte SANS mot de passe. Ce n'est pas un compte ouvert :
c'est un compte qui ne peut pas se connecter à l'interface web, et qui n'existe
que pour porter des tokens d'API. Un choix légitime, à condition d'être un choix.

Ce que ce compte pourra faire est décidé APRÈS, par une ACL — un compte fraîchement
créé n'a aucun droit, pas même de se voir :

  pvecli pool create collegue
  pvecli access acl set --path /pool/collegue --role PVEVMAdmin --user collegue@pve

Privilèges exigés par le nœud : « Realm.AllocateUser » sur
« /access/realm/<realm> » et « User.Modify » sur « /access/groups ». Un token
d'audit ne les a pas, et c'est voulu.

Endpoint : POST /api2/json/access/users`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]
			if !strings.Contains(userID, "@") {
				return &exitError{
					code: pve.ExitUsage,
					msg: fmt.Sprintf("« %s » n'a pas de realm — il en faut un : %s@pve\n"+
						"  pvecli access user ls     (pour voir les realms en usage)", userID, userID),
				}
			}
			if expire == "" && !noExpire {
				return &exitError{
					code: pve.ExitUsage,
					msg: "--expire est obligatoire : un accès prêté qui n'expire pas devient un accès permanent\n" +
						"que personne n'a décidé d'accorder.\n" +
						"  --expire 2026-12-31     (ou --no-expire, si c'est un choix)",
				}
			}
			epoch, err := parseExpiry(expire, noExpire)
			if err != nil {
				return err
			}

			password, err := resolveNewPassword(cmd, noPassword)
			if err != nil {
				return err
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			o := pve.UserOptions{
				Comment: comment, Email: email, Groups: groups,
				Password: password, Expire: epoch, Disable: disable,
			}
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: userID,
				// Creating an identity is not destructive, but it is an identity:
				// the confirmation follows the consequence, not the verb.
				Destructive: true,
				Plan: service.Plan{
					Method: "POST",
					Path:   pve.UserPath(),
					// Redacted, and only here: the payload this project prints is
					// the real one everywhere else. A password on a terminal ends
					// up in a scrollback.
					Payload:  o.Redacted(userID),
					Effect:   fmt.Sprintf("compte « %s » créé, expire %s, AUCUN droit", userID, expiryLabel(epoch)),
					Rollback: "suppression depuis l'interface — DELETE /access/users n'est pas implémenté ici",
					Verify:   "relecture du compte",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.UserInfo(ctx, userID); err == nil {
						return service.State{}, fmt.Errorf(
							"le compte « %s » existe déjà — le recréer écraserait ses attributs", userID)
					}
					return service.State{Exists: true, Status: "identifiant libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.CreateUser(ctx, userID, o)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					info, err := client.UserInfo(ctx, userID)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "créé", Raw: info}, nil
				},
			})
			if err != nil {
				return err
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun {
				// A new account can do strictly nothing. Saying so here avoids the
				// next half-hour spent wondering why it cannot even list a VM.
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\n« %s » n'a encore AUCUN droit — pas même de se lister. Ensuite :\n"+
						"  pvecli access acl set --path /pool/<pool> --role PVEVMAdmin --user %s\n", userID, userID)
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"utilisateur", userID},
				{"état", result.Status},
				{"expire", expiryLabel(epoch)},
				{"mot de passe", yesNo(password != "")},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.StringVar(&comment, "comment", "", "commentaire — dis à quoi sert ce compte")
	f.StringVar(&email, "email", "", "adresse e-mail du compte")
	f.StringVar(&groups, "groups", "", "groupes, séparés par des virgules")
	f.StringVar(&expire, "expire", "", "date d'expiration, AAAA-MM-JJ")
	f.BoolVar(&noExpire, "no-expire", false, "compte sans expiration — à assumer")
	f.BoolVar(&disable, "disable", false, "crée le compte désactivé")
	f.BoolVar(&noPassword, "no-password", false, "compte sans mot de passe : ne peut pas se connecter à l'interface web")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// resolveNewPassword gets the initial password from the environment, or asks
// for it on a terminal. It is never a flag, and never echoed.
//
// Refusing when there is neither a variable nor a terminal is deliberate: the
// alternative is creating a passwordless account in a script that believed it
// was setting one.
func resolveNewPassword(cmd *cobra.Command, noPassword bool) (string, error) {
	if noPassword {
		return "", nil
	}
	if fromEnv := os.Getenv(EnvNewUserPassword); fromEnv != "" {
		if len(fromEnv) < 8 {
			return "", &exitError{code: pve.ExitUsage,
				msg: EnvNewUserPassword + " fait moins de 8 caractères — le nœud refusera"}
		}
		return fromEnv, nil
	}
	if !stdinIsTerminal() {
		return "", &exitError{
			code: pve.ExitConfirm,
			msg: "aucun mot de passe et aucun terminal pour le demander.\n" +
				"  export " + EnvNewUserPassword + "=\"…\"\n" +
				"  ou --no-password, pour un compte qui ne portera que des tokens",
		}
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Mot de passe initial (8 caractères minimum, non affiché) : ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}
	password := string(raw)
	if len(password) < 8 {
		return "", &exitError{code: pve.ExitUsage, msg: "mot de passe trop court : 8 caractères minimum"}
	}
	return password, nil
}

// expiryLabel turns an epoch into something an operator can act on. A raw
// timestamp is technically the truth and practically useless.
func expiryLabel(epoch int64) string {
	if epoch == 0 {
		return "jamais"
	}
	when := time.Unix(epoch, 0)
	days := int(time.Until(when).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("%s (EXPIRÉ)", when.Format("2006-01-02"))
	case days <= 30:
		return fmt.Sprintf("%s (dans %d j)", when.Format("2006-01-02"), days)
	default:
		return when.Format("2006-01-02")
	}
}

// ---------------------------------------------------------------- roles

func newRoleCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "role",
		Short: "Rôles et privilèges qu'ils accordent",
		Args:  usage(cobra.NoArgs),
	}

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les rôles (GET /access/roles)",
		Args:    usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			roles, err := client.Roles(cmd.Context())
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"RÔLE", "INTÉGRÉ", "PRIVILÈGES"}}
			for _, r := range roles {
				rows.Cells = append(rows.Cells, []string{
					r.RoleID, yesNo(r.IsBuiltin()), fmt.Sprintf("%d", len(r.Privileges())),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, roles, rows)
		},
	}
	addRenderFlags(ls)

	show := &cobra.Command{
		Use:   "show <rôle>",
		Short: "Détaille les privilèges d'un rôle (GET /access/roles/{roleid})",
		Long: `Liste les privilèges d'un rôle.

À lire AVANT d'attribuer le rôle : c'est la seule façon de savoir ce qu'on
accorde. « PVEVMAdmin » ne dit pas qu'il contient VM.Snapshot.Rollback.

L'index et le détail ne renvoient pas la même forme — une chaîne à virgules
d'un côté, une map de l'autre. Même information, deux schémas.

Endpoint : GET /api2/json/access/roles/{roleid}`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			privs, err := client.RolePrivileges(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"PRIVILÈGE"}}
			for _, p := range privs {
				rows.Cells = append(rows.Cells, []string{p})
			}
			return output.Render(cmd.OutOrStdout(), opts, privs, rows)
		},
	}
	addRenderFlags(show)

	c.AddCommand(ls, show)
	return c
}

// ---------------------------------------------------------------- acl

func newACLCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "acl",
		Short: "Liste et modifie les ACL",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newACLListCmd(), newACLSetCmd())
	return c
}

func newACLListCmd() *cobra.Command {
	var path string

	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les ACL (GET /access/acl)",
		Long: `Liste les entrées de contrôle d'accès : (chemin, identité, rôle, propagation).

Triées du chemin le plus général au plus spécifique, parce que c'est dans cet
ordre que les privilèges se transmettent — mais ils ne s'ADDITIONNENT pas. Une
ACL posée sur un chemin plus profond REMPLACE les rôles hérités du parent, elle
ne s'y ajoute pas (PVE::AccessControl::roles, « overwrite previous settings »).
Poser PVEUserAdmin sur /access à une identité qui avait PVEAuditor sur / lui
retire donc ses droits de lecture sur /access. Pour cumuler, il faut les deux
rôles sur le MÊME chemin.

La liste est filtrée par le nœud : elle ne montre que les objets dont on a le
droit de modifier les permissions. Vide ne veut donc pas dire « aucune ACL ».

Endpoint : GET /api2/json/access/acl`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			entries, err := client.ACL(cmd.Context())
			if err != nil {
				return err
			}
			if path != "" {
				entries = entriesOnPath(entries, path)
			}

			// An empty answer is not "there is no ACL": the node restricts the
			// listing to objects whose permissions the caller may modify. Left
			// unsaid, an empty table reads as a node with no access control at
			// all — the most reassuring possible way to be wrong.
			if len(entries) == 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"aucune ACL VISIBLE — le nœud ne montre que celles que tu as le droit de\n"+
						"modifier. Il en existe probablement d'autres. Il faut Sys.Audit sur /access\n"+
						"(ou Permissions.Modify) pour les voir toutes :\n"+
						"  pvecli access whoami --path /access\n")
			}

			rows := output.Rows{Headers: []string{"CHEMIN", "TYPE", "IDENTITÉ", "RÔLE", "PROPAGE"}}
			for _, e := range entries {
				rows.Cells = append(rows.Cells, []string{
					e.Path, e.Type, e.UGID, e.RoleID, yesNo(e.Propagate == 1),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, entries, rows)
		},
	}

	c.Flags().StringVar(&path, "path", "", "ne montre que ce chemin")
	addRenderFlags(c)
	return c
}

func entriesOnPath(entries []pve.ACLEntry, path string) []pve.ACLEntry {
	kept := entries[:0]
	for _, e := range entries {
		if e.Path == path {
			kept = append(kept, e)
		}
	}
	return kept
}

func newACLSetCmd() *cobra.Command {
	var (
		change   pve.ACLChange
		iKnow    bool
		noPropag bool
	)

	c := &cobra.Command{
		Use:   "set",
		Short: "Pose ou retire une ACL (PUT /access/acl)",
		Long: `Attribue un rôle à une identité sur un chemin — ou le retire avec --delete.

Le principe de moindre privilège s'applique dans le TEMPS : on élargit quand on
en a besoin, pas « au cas où ». La progression prévue par ce projet :

  M0   PVEAuditor          sur /                lecture seule
  M3   PVEVMAdmin          sur /vms             cycle de vie des guests
  M5   PVEDatastoreAdmin   sur /storage/...     sauvegardes et restaurations

On ne peut pas donner ce qu'on n'a pas : sans le privilège Permissions.Modify,
PVE n'accepte l'attribution d'un rôle que si l'appelant détient déjà TOUS les
privilèges de ce rôle, avec propagation. Ce n'est pas une politique de cette
CLI, c'est le code du nœud (PVE/API2/ACL.pm).

L'API nomme ses paramètres au pluriel — roles, users, tokens — parce qu'un
appel peut en porter plusieurs. Cette commande n'en expose qu'un à la fois :
une ACL qui touche quatre identités d'un coup n'est relisible par personne.

Endpoint : PUT /api2/json/access/acl`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if change.Path == "" || change.Role == "" {
				return &exitError{code: pve.ExitUsage, msg: "--path et --role sont obligatoires"}
			}
			if change.Identity() == "" {
				return &exitError{code: pve.ExitUsage, msg: "il faut une identité : --user, --token ou --group"}
			}
			// Propagation defaults to on, like the API's own schema, but
			// --no-propagate has to be able to turn it off.
			change.Propagate = !noPropag

			if change.Role == "Administrator" && change.Path == "/" && !change.Delete && !iKnow {
				return &exitError{
					code: pve.ExitUsage,
					msg: "Administrator sur « / » donne tout, partout, y compris le droit de\n" +
						"modifier les ACL — c'est root@pam avec un autre nom.\n" +
						"L'alternative est presque toujours un rôle ciblé sur un chemin précis :\n" +
						"  pvecli access acl set --path /vms --role PVEVMAdmin --token …\n" +
						"Si c'est vraiment ce que tu veux : --i-know-what-im-doing",
				}
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			verb := "attribution"
			if change.Delete {
				verb = "retrait"
			}
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target:      change.Path,
				Destructive: change.Delete,
				Plan: service.Plan{
					Method:   "PUT",
					Path:     pve.ACLPath(),
					Payload:  change.Values(),
					Effect:   fmt.Sprintf("%s du rôle %s à %s sur %s", verb, change.Role, change.Identity(), change.Path),
					Rollback: rollbackACL(change),
					Verify:   "relecture de GET /access/acl",
				},
				// The pre-read shows what is already granted on this path:
				// an ACL change with no "before" is a change nobody can review.
				PreRead: func(ctx context.Context) (service.State, error) {
					entries, err := client.ACL(ctx)
					if err != nil {
						return service.State{}, err
					}
					before := entriesOnPath(entries, change.Path)
					printACLBefore(cmd, change.Path, before)
					return service.State{Exists: true, Status: "lue", Raw: before}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					// PUT /access/acl is synchronous: no UPID, nothing to poll.
					return "", client.SetACL(ctx, change)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					entries, err := client.ACL(ctx)
					if err != nil {
						return service.State{}, err
					}
					after := entriesOnPath(entries, change.Path)
					present := aclHas(after, change)
					if change.Delete && present {
						return service.State{}, fmt.Errorf("l'entrée est toujours là après le retrait")
					}
					if !change.Delete && !present {
						return service.State{}, fmt.Errorf("l'entrée est absente après l'attribution")
					}
					return service.State{Exists: true, Status: verb + " effectué", Raw: after}, nil
				},
			})
			if err != nil {
				return err
			}

			entries, _ := result.Raw.([]pve.ACLEntry)
			rows := output.Rows{Headers: []string{"CHEMIN", "TYPE", "IDENTITÉ", "RÔLE", "PROPAGE"}}
			for _, e := range entries {
				rows.Cells = append(rows.Cells, []string{
					e.Path, e.Type, e.UGID, e.RoleID, yesNo(e.Propagate == 1),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.StringVar(&change.Path, "path", "", "chemin ACL, ex. /vms/120")
	f.StringVar(&change.Role, "role", "", "rôle à attribuer — vois « pvecli access role show »")
	f.StringVar(&change.User, "user", "", "utilisateur cible, ex. automation@pve")
	f.StringVar(&change.Token, "token", "", "token cible, ex. automation@pve!pvectl")
	f.StringVar(&change.Group, "group", "", "groupe cible")
	f.BoolVar(&noPropag, "no-propagate", false, "n'applique le rôle qu'à ce chemin exact")
	f.BoolVar(&change.Delete, "delete", false, "retire l'entrée au lieu de l'ajouter")
	f.BoolVar(&iKnow, "i-know-what-im-doing", false, "lève le refus d'Administrator sur /")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func rollbackACL(change pve.ACLChange) string {
	if change.Delete {
		return fmt.Sprintf("pvecli access acl set --path %s --role %s …", change.Path, change.Role)
	}
	return fmt.Sprintf("pvecli access acl set --path %s --role %s … --delete", change.Path, change.Role)
}

// printACLBefore shows the state of the targeted path, so the diff is visible
// rather than implied.
func printACLBefore(cmd *cobra.Command, path string, before []pve.ACLEntry) {
	if len(before) == 0 {
		// "visible" is not a hedge: GET /access/acl only returns entries whose
		// permissions the caller may modify. Saying "aucune ACL" would state as
		// a fact something this identity cannot know.
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\navant : aucune ACL VISIBLE sur %s\n", path)
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\navant, sur %s :\n", path)
	for _, e := range before {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %-8s %-28s %-20s propage=%s\n",
			e.Type, e.UGID, e.RoleID, yesNo(e.Propagate == 1))
	}
}

func aclHas(entries []pve.ACLEntry, change pve.ACLChange) bool {
	for _, e := range entries {
		if e.RoleID == change.Role && e.UGID == change.Identity() {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- tokens

func newTokenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "token",
		Short: "Tokens d'API d'un utilisateur",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newTokenListCmd(), newTokenCreateCmd(), newTokenRemoveCmd())
	return c
}

func newTokenListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls <utilisateur>",
		Aliases: []string{"list"},
		Short:   "Liste les tokens d'un utilisateur (GET /access/users/{userid}/token)",
		Long: `Liste les tokens d'un utilisateur.

Le secret n'y figure pas, et ne peut pas y figurer : PVE ne le stocke que haché
et ne le rend qu'une fois, à la création.

Endpoint : GET /api2/json/access/users/{userid}/token`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			tokens, err := client.Tokens(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"TOKEN", "PRIVSEP", "EXPIRE", "COMMENTAIRE"}}
			for _, t := range tokens {
				rows.Cells = append(rows.Cells, []string{
					t.TokenID, yesNo(t.Separated()), expiryLabel(t.Expire.Int()), t.Comment,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, tokens, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newTokenCreateCmd() *cobra.Command {
	var (
		expire   string
		noExpire bool
		comment  string
		noPriv   bool
	)

	c := &cobra.Command{
		Use:   "create <utilisateur> <nom-du-token>",
		Short: "Délivre un token d'API (POST /access/users/{userid}/token/{tokenid})",
		Long: `Délivre un token d'API dédié.

LE SECRET N'EST RENDU QU'UNE FOIS. Il part seul sur la sortie standard, sans
décoration, pour que « > /tmp/secret » produise un fichier utilisable. Il n'est
jamais réaffichable : PVE ne le stocke que haché.

  pvecli access token create automation@pve terraform --expire 2026-12-31 > s

--expire est OBLIGATOIRE, sauf --no-expire explicite : un token qui ne meurt
jamais doit être un choix, pas un oubli. L'API attend des secondes depuis
l'epoch ; la conversion se fait ici et se voit en --dry-run.

privsep=1 par défaut : le token n'a que les droits de ses propres ACL,
intersectés avec ceux de son utilisateur. --no-privsep lui donne TOUS les
droits de l'utilisateur, ce qui annule l'intérêt d'un token dédié.

Endpoint : POST /api2/json/access/users/{userid}/token/{tokenid}`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID, tokenID := args[0], args[1]

			if expire == "" && !noExpire {
				return &exitError{
					code: pve.ExitUsage,
					msg: "--expire est obligatoire : un token sans expiration survit à l'usage\n" +
						"qui l'a motivé, et à la personne qui s'en souvenait.\n" +
						"  --expire 2026-12-31     (ou --no-expire, si c'est un choix)",
				}
			}
			epoch, err := parseExpiry(expire, noExpire)
			if err != nil {
				return err
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			o := pve.TokenOptions{Comment: comment, Expire: epoch, Separated: !noPriv}
			var created *pve.NewToken
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: fmt.Sprintf("%s!%s", userID, tokenID),
				// Not destructive, but sensitive: it mints a credential. The
				// confirmation level follows the consequence, not the verb.
				Destructive: true,
				Plan: service.Plan{
					Method:   "POST",
					Path:     pve.TokenPath(userID, tokenID),
					Payload:  o.Values(),
					Effect:   fmt.Sprintf("token « %s » délivré à %s, expire %s", tokenID, userID, expiryLabel(epoch)),
					Rollback: fmt.Sprintf("pvecli access token rm %s %s", userID, tokenID),
					Verify:   "relecture du token (sans son secret)",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.TokenInfo(ctx, userID, tokenID); err == nil {
						return service.State{}, fmt.Errorf("le token « %s » existe déjà pour %s — choisis un autre nom", tokenID, userID)
					}
					return service.State{Exists: true, Status: "nom libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					created, err = client.CreateToken(ctx, userID, tokenID, o)
					if err != nil {
						return "", err
					}
					return "", nil
				},
				// The proof is a re-read that does NOT contain the secret: the
				// token exists, and saying so does not print it twice.
				PostRead: func(ctx context.Context) (service.State, error) {
					info, err := client.TokenInfo(ctx, userID, tokenID)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "délivré", Raw: info}, nil
				},
			})

			// The secret is printed BEFORE the error is returned, and that
			// order is the whole point. It comes back exactly once; if the
			// post-read then fails, returning early would leave a live
			// credential on the node that nobody holds. Learned the hard way:
			// a decoding error after the write did exactly that.
			if created != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"token %s délivré. Le secret ci-dessous ne sera PLUS JAMAIS affiché :\n", created.FullTokenID)
				// Alone, undecorated, on stdout.
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), created.Value)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"  PVE_API_TOKEN_ID=%s\n  PVE_API_TOKEN_SECRET=<ci-dessus>\n", created.FullTokenID)
			}
			if err != nil {
				return err
			}
			if created == nil {
				// --dry-run: nothing was minted, so there is nothing to print.
				rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
					{"utilisateur", userID}, {"token", tokenID}, {"expire", expiryLabel(epoch)},
				}}
				return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
			}
			return nil
		},
	}

	f := c.Flags()
	f.StringVar(&expire, "expire", "", "date d'expiration, AAAA-MM-JJ")
	f.BoolVar(&noExpire, "no-expire", false, "token sans expiration — à assumer explicitement")
	f.StringVar(&comment, "comment", "", "commentaire attaché au token")
	f.BoolVar(&noPriv, "no-privsep", false, "donne au token tous les droits de son utilisateur")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// parseExpiry converts a date into the epoch seconds the API wants.
func parseExpiry(value string, noExpire bool) (int64, error) {
	if noExpire {
		return 0, nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if when, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return when.Unix(), nil
		}
	}
	return 0, &exitError{
		code: pve.ExitUsage,
		msg:  fmt.Sprintf("--expire attend une date AAAA-MM-JJ, reçu %q", value),
	}
}

func newTokenRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <utilisateur> <nom-du-token>",
		Aliases: []string{"delete", "revoke"},
		Short:   "Révoque un token (DELETE /access/users/{userid}/token/{tokenid})",
		Long: `Révoque un token d'API.

Tout ce qui l'utilisait perd l'accès immédiatement. Il n'existe aucun moyen de
le rétablir : un nouveau token porte un nouveau secret.

Endpoint : DELETE /api2/json/access/users/{userid}/token/{tokenid}`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID, tokenID := args[0], args[1]
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			runner := newRunner(cmd, client)

			_, err = runner.Run(cmd.Context(), service.Mutation{
				Target:      tokenID,
				Destructive: true,
				Plan: service.Plan{
					Method:   "DELETE",
					Path:     pve.TokenPath(userID, tokenID),
					Effect:   fmt.Sprintf("révocation du token « %s » de %s", tokenID, userID),
					Rollback: "aucun — un nouveau token porte un nouveau secret",
					Verify:   "le token doit avoir disparu de la liste",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					info, err := client.TokenInfo(ctx, userID, tokenID)
					if err != nil {
						return service.State{}, fmt.Errorf("le token « %s » n'existe pas pour %s", tokenID, userID)
					}
					return service.State{Exists: true, Status: "présent", Raw: info}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.DeleteToken(ctx, userID, tokenID)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.TokenInfo(ctx, userID, tokenID); err == nil {
						return service.State{}, fmt.Errorf("le token « %s » répond encore", tokenID)
					}
					return service.State{Exists: false, Status: "révoqué"}, nil
				},
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "token « %s » de %s révoqué.\n", tokenID, userID)
			return nil
		},
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}
