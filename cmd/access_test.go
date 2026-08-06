package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/testutil"
)

// `--can` exists to be used in a shell `if`. Its contract is therefore an exit
// code, and a stdout carrying one word and nothing else.
func TestCanAnswersOnStdoutAndExitCode(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/access/permissions": "permissions.json",
	})
	point(t, srv.URL)

	stdout, _, err := run(t, "access", "whoami", "--path", "/vms", "--can", "VM.PowerMgmt")
	if err != nil {
		t.Fatalf("un privilège détenu doit sortir en 0 : %v", err)
	}
	if strings.TrimSpace(stdout) != "oui" {
		t.Errorf("stdout = %q, want \"oui\"", stdout)
	}

	stdout, _, err = run(t, "access", "whoami", "--path", "/vms", "--can", "Permissions.Modify")
	if strings.TrimSpace(stdout) != "non" {
		t.Errorf("stdout = %q, want \"non\"", stdout)
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 1 {
		t.Errorf("un privilège absent doit sortir en 1, got %v", err)
	}
}

// --can without --path cannot be answered: a privilege is held ON a path.
// Refusing is more useful than answering "non" about nothing.
func TestCanRequiresAPath(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/access/permissions": "permissions.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "whoami", "--can", "VM.PowerMgmt")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("--can sans --path est une erreur d'usage, got %v", err)
	}
}

// Administrator on "/" is root@pam under another name. The guard is the whole
// point of PVX-035: the CLI has to make the good practice easier than the bad
// one, and this is the bad one.
func TestAdministratorOnRootIsRefused(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/access/acl": "acl.json",
		"PUT /api2/json/access/acl": "upid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "acl", "set", "--path", "/", "--role", "Administrator",
		"--user", "automation@pve", "--yes")
	if err == nil {
		t.Fatal("Administrator sur / doit être refusé sans le drapeau")
	}
	if !strings.Contains(err.Error(), "i-know-what-im-doing") {
		t.Errorf("le refus doit nommer la porte de sortie : %v", err)
	}
	for _, req := range srv.Requests {
		if strings.HasPrefix(req, "PUT ") {
			t.Fatalf("le refus doit précéder toute écriture : %v", srv.Requests)
		}
	}

	// Narrower targets go through: the guard is about "/", not about the role
	// existing at all.
	if _, _, err := run(t, "access", "acl", "set", "--path", "/vms/120", "--role", "PVEVMAdmin",
		"--user", "automation@pve", "--dry-run"); err != nil {
		t.Errorf("un rôle ciblé sur un chemin précis ne doit pas être bloqué : %v", err)
	}
}

// A token that never expires has to be a decision, not an oversight.
func TestTokenCreateDemandsAnExpiry(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "access", "token", "create", "automation@pve", "terraform")
	if err == nil || !strings.Contains(err.Error(), "--no-expire") {
		t.Errorf("--expire doit être exigé, avec sa porte de sortie : %v", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir : %v", srv.Requests)
	}
}

// The plan of a token creation is printed on stderr. It must not carry the
// secret — and neither must a --verbose trace of the same command.
func TestTokenPlanNeverPrintsASecret(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, stderr, _ := run(t, "-vv", "access", "token", "create", "automation@pve", "terraform",
		"--expire", "2026-12-31", "--dry-run")

	if strings.Contains(stderr, "s3cr3t") {
		t.Errorf("le secret du token courant a fuité dans le plan ou la trace :\n%s", stderr)
	}
}

// A realm is part of an identity: "collegue" and "collegue@pve" are not the
// same thing, and the second is the only one the node understands.
func TestUserCreateDemandsARealm(t *testing.T) {
	_, _, err := run(t, "access", "user", "create", "collegue", "--no-expire", "--no-password")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("un identifiant sans realm est une erreur d'usage, got %v", err)
	}
}

// Same reasoning as the API token: an access lent without an expiry becomes a
// permanent access nobody decided to grant.
func TestUserCreateDemandsAnExpiry(t *testing.T) {
	_, _, err := run(t, "access", "user", "create", "collegue@pve", "--no-password")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("--expire manquant est une erreur d'usage, got %v", err)
	}
}

// Without a variable and without a terminal, the only alternatives are refusing
// or silently creating a passwordless account. Refusing is the one that does
// not surprise anyone three weeks later.
func TestUserCreateRefusesWhenItCannotAskForAPassword(t *testing.T) {
	t.Setenv(EnvNewUserPassword, "")
	_, _, err := run(t, "access", "user", "create", "collegue@pve", "--no-expire")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitConfirm {
		t.Errorf("sans mot de passe ni terminal, il faut refuser en 5, got %v", err)
	}
}

// The node refuses under 8 characters. Finding that out after the pre-read has
// already run is a round trip for nothing.
func TestUserCreateRefusesAShortPassword(t *testing.T) {
	t.Setenv(EnvNewUserPassword, "court")
	_, _, err := run(t, "access", "user", "create", "collegue@pve", "--no-expire")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("un mot de passe trop court est une erreur d'usage, got %v", err)
	}
}

// The plan a --dry-run prints is the real payload everywhere in this project —
// except here, where printing it would put a password in a scrollback.
func TestUserOptionsRedactThePassword(t *testing.T) {
	o := pve.UserOptions{Password: "s3cr3t-long"}

	if got := o.Values("collegue@pve").Get("password"); got != "s3cr3t-long" {
		t.Errorf("le payload réel doit porter le mot de passe, got %q", got)
	}
	if got := o.Redacted("collegue@pve").Get("password"); got == "s3cr3t-long" {
		t.Error("le payload affiché ne doit pas porter le mot de passe")
	}
	// A user with no password has nothing to redact, and no empty key to send.
	if _, present := (pve.UserOptions{}).Values("collegue@pve")["password"]; present {
		t.Error("aucun paramètre password ne doit partir quand il n'y en a pas")
	}
}

// -------------------------------------------------- écritures de rôle (PVX-077)

// roleWrite enregistre le CORPS des requêtes, ce que testutil ne fait pas. Les
// refus et les --dry-run se vérifient sur le plan ; ce qui part vraiment sur le
// réseau, non — et c'est là que vit le piège d'« append ».
type roleWrite struct {
	method string
	path   string
	form   url.Values
}

// adminRolePrivs est l'univers des privilèges d'un nœud, tel que
// GET /access/roles/Administrator le rend : une map privilège→1. C'est la
// source de vérité de la validation, à la place d'une liste codée en dur.
const adminRolePrivs = `{"data":{"Sys.Audit":1,"Sys.Modify":1,"Datastore.Allocate":1,` +
	`"VM.Backup":1,"Permissions.Modify":1,"Sys.AccessNetwork":1}}`

// roleAbsent fait répondre 500 comme le nœud le fait sur un rôle inconnu.
// « pas d'erreur » ne veut pas dire « existe » : un {"data":null} se décode en
// map vide sans broncher, et la pré-lecture d'un « add » y lirait un rôle
// existant.
const roleAbsent = "\x00absent"

// roleWriteServer répond aux lectures et retient les écritures.
//
// Il est volontairement à ÉTAT : après un DELETE, le chemin supprimé répond
// 500, comme le nœud le fait sur un rôle inconnu. Sans ça, le post-read d'un
// « rm » verrait le rôle répondre encore et la commande ne prouverait rien.
func roleWriteServer(t *testing.T, answers map[string]string) (*httptest.Server, *[]roleWrite) {
	t.Helper()
	var calls []roleWrite
	deleted := map[string]bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls = append(calls, roleWrite{method: r.Method, path: r.URL.Path, form: r.PostForm})
		if r.Method == http.MethodDelete {
			deleted[r.URL.Path] = true
		}
		w.Header().Set("Content-Type", "application/json")
		if deleted[r.URL.Path] && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"data":null,"errors":{"roleid":"no such role"}}`))
			return
		}
		body, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			body = `{"data":null}`
		}
		if body == roleAbsent {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"data":null,"errors":{"roleid":"no such role"}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func roleWriteOf(t *testing.T, calls *[]roleWrite, method string) roleWrite {
	t.Helper()
	for _, c := range *calls {
		if c.method == method {
			return c
		}
	}
	t.Fatalf("aucune requête %s émise : %+v", method, *calls)
	return roleWrite{}
}

func noRoleWrite(t *testing.T, calls *[]roleWrite) {
	t.Helper()
	for _, c := range *calls {
		if c.method != http.MethodGet {
			t.Fatalf("le refus doit précéder toute écriture : %+v", *calls)
		}
	}
}

// Un rôle sans privilège est « NoAccess » sous un autre nom : il s'attribue, et
// il n'accorde rien. L'API l'accepte ; cette CLI ne le fabrique pas.
func TestRoleAddDemandsPrivileges(t *testing.T) {
	srv, calls := roleWriteServer(t, nil)
	point(t, srv.URL)

	_, _, err := run(t, "access", "role", "add", "ops-backup", "--yes")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Fatalf("--privs manquant est une erreur d'usage, got %v", err)
	}
	noRoleWrite(t, calls)
}

// « PVE » est un espace de noms réservé, et le refus vient du NŒUD :
// PVE::API2::Role::create_role rejette « /^PVE/i ». Le préfixe est comparé sans
// tenir compte de la casse — d'où « pveBackupOps » dans la table, qui est le cas
// qu'une vérification locale naïve laisse passer jusqu'à l'écriture.
//
// Conséquence directe et contre-intuitive : un rôle ne peut PAS s'appeler
// « PVEBackupJobAdmin ». Le test le fige, pour que personne ne le redécouvre
// contre un vrai nœud.
func TestRoleAddRefusesTheReservedPVENamespace(t *testing.T) {
	for _, name := range []string{"PVEBackupOps", "PVEBackupJobAdmin", "pveBackupOps", "Administrator"} {
		srv, calls := roleWriteServer(t, nil)
		point(t, srv.URL)

		_, _, err := run(t, "access", "role", "add", name, "--privs", "Sys.Audit", "--yes")
		var coded interface{ ExitCode() int }
		if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
			t.Fatalf("%q doit être refusé en erreur d'usage, got %v", name, err)
		}
		if !strings.Contains(err.Error(), "espace") {
			t.Errorf("%q : le refus doit nommer l'espace réservé, got %v", name, err)
		}
		noRoleWrite(t, calls)
	}
}

// LE test de cette famille. Le PUT REMPLACE la liste ; « append » ferait
// l'union côté nœud, donc un --dry-run ne pourrait pas montrer le résultat.
// pvecli lit, fusionne ici, et envoie la liste complète — sans « append ».
func TestRoleSetAddPrivSendsTheUnionAndNeverAppends(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/Administrator": adminRolePrivs,
		"GET /api2/json/access/roles/ops-backup":    `{"data":{"Sys.Audit":1}}`,
	})
	point(t, srv.URL)

	if _, _, err := run(t, "access", "role", "set", "ops-backup", "--add-priv", "Sys.Modify", "--yes"); err != nil {
		t.Fatalf("access role set: %v", err)
	}

	put := roleWriteOf(t, calls, http.MethodPut)
	if put.path != "/api2/json/access/roles/ops-backup" {
		t.Errorf("chemin = %q", put.path)
	}
	if got := put.form.Get("privs"); got != "Sys.Audit,Sys.Modify" {
		t.Fatalf("privs = %q — l'union doit être calculée AVANT l'écriture", got)
	}
	if _, present := put.form["append"]; present {
		t.Errorf("« append » ne doit jamais partir : %v", put.form)
	}
}

// L'API n'a AUCUNE primitive de retrait : sans read-merge-write, retirer un
// privilège est impossible. Et la perte est traitée comme destructive, parce
// que les identités qui portent le rôle la subissent sans être relues.
func TestRoleSetRmPrivAmputatesTheListAndSaysWhatIsLost(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/Administrator": adminRolePrivs,
		"GET /api2/json/access/roles/ops-backup":    `{"data":{"Sys.Audit":1,"Sys.Modify":1}}`,
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "access", "role", "set", "ops-backup", "--rm-priv", "Sys.Modify", "--yes")
	if err != nil {
		t.Fatalf("access role set: %v", err)
	}
	if got := roleWriteOf(t, calls, http.MethodPut).form.Get("privs"); got != "Sys.Audit" {
		t.Fatalf("privs = %q", got)
	}
	if !strings.Contains(stderr, "- Sys.Modify") {
		t.Errorf("le pre-read doit nommer le privilège retiré :\n%s", stderr)
	}
	if !strings.Contains(stderr, "PERDUS") {
		t.Errorf("une perte de privilège doit être annoncée :\n%s", stderr)
	}
}

// --privs REMPLACE : oublier un privilège dans la liste le supprime. C'est la
// même perte qu'un --rm-priv, et elle s'annonce pareil.
func TestRoleSetPrivsAnnouncesWhatItDrops(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/Administrator": adminRolePrivs,
		"GET /api2/json/access/roles/ops-backup":    `{"data":{"Sys.Audit":1,"Sys.Modify":1}}`,
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "access", "role", "set", "ops-backup", "--privs", "Sys.Audit", "--yes")
	if err != nil {
		t.Fatalf("access role set: %v", err)
	}
	if got := roleWriteOf(t, calls, http.MethodPut).form.Get("privs"); got != "Sys.Audit" {
		t.Fatalf("privs = %q", got)
	}
	if !strings.Contains(stderr, "- Sys.Modify") {
		t.Errorf("le privilège perdu doit être nommé :\n%s", stderr)
	}
}

// « que doit contenir ce rôle » et « qu'est-ce qui change » sont deux questions
// différentes. Les mélanger rendrait le résultat dépendant d'un ordre que
// personne ne lit.
func TestRoleSetRefusesPrivsCombinedWithAddPriv(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/ops-backup": `{"data":{"Sys.Audit":1}}`,
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "role", "set", "ops-backup",
		"--privs", "Sys.Audit", "--add-priv", "Sys.Modify", "--yes")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Fatalf("--privs avec --add-priv est une erreur d'usage, got %v", err)
	}
	noRoleWrite(t, calls)
}

// Sans drapeau, il n'y a rien à écrire — et l'aller-retour se refuse avant la
// moindre lecture.
func TestRoleSetRefusesANoOp(t *testing.T) {
	srv, calls := roleWriteServer(t, nil)
	point(t, srv.URL)

	_, _, err := run(t, "access", "role", "set", "ops-backup", "--yes")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Fatalf("une modification vide est une erreur d'usage, got %v", err)
	}
	noRoleWrite(t, calls)
}

// Un rôle vidé de ses privilèges reste attribué et n'accorde plus rien : c'est
// NoAccess sous un autre nom, la forme la plus rassurante de la panne.
func TestRoleSetRefusesToEmptyTheRole(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/Administrator": adminRolePrivs,
		"GET /api2/json/access/roles/ops-backup":    `{"data":{"Sys.Audit":1}}`,
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "role", "set", "ops-backup", "--rm-priv", "Sys.Audit", "--yes")
	if err == nil || !strings.Contains(err.Error(), "access role rm") {
		t.Fatalf("vider un rôle doit renvoyer vers sa suppression : %v", err)
	}
	noRoleWrite(t, calls)
}

// Une faute de frappe passerait jusqu'au rôle, qui n'accorderait rien tout en
// ayant l'air correct. La liste des privilèges connus vient du nœud
// (Administrator), jamais d'une liste codée en dur.
func TestRoleWritesRefuseAnUnknownPrivilege(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/Administrator": adminRolePrivs,
	})
	point(t, srv.URL)

	_, _, err := run(t, "access", "role", "add", "ops-backup", "--privs", "Sys.Modiy", "--yes")
	if err == nil || !strings.Contains(err.Error(), "inconnu") {
		t.Fatalf("un privilège inconnu doit être refusé : %v", err)
	}
	noRoleWrite(t, calls)

	// La casse compte côté nœud, et le refus doit donner la bonne graphie
	// plutôt que laisser chercher.
	_, _, err = run(t, "access", "role", "add", "ops-backup", "--privs", "Sys.modify", "--yes")
	if err == nil || !strings.Contains(err.Error(), "Sys.Modify") {
		t.Fatalf("la casse doit être corrigée dans le refus : %v", err)
	}
	noRoleWrite(t, calls)
}

// La vérité sur un rôle intégré vient du nœud (« special »), le nom n'est qu'un
// filet. Les deux doivent refuser.
func TestRoleRmRefusesABuiltinRole(t *testing.T) {
	srv, calls := roleWriteServer(t, nil)
	point(t, srv.URL)

	_, _, err := run(t, "access", "role", "rm", "PVEAuditor", "--yes")
	if err == nil || !strings.Contains(err.Error(), "i-know-what-im-doing") {
		t.Fatalf("un rôle intégré doit être refusé, avec sa porte de sortie : %v", err)
	}
	noRoleWrite(t, calls)
}

// Supprimer un rôle retire des droits à tout le monde d'un coup : une ACL
// n'accorde qu'un rôle. Le pre-read doit dire à qui — et dire aussi que la
// liste est FILTRÉE par les droits de l'appelant.
func TestRoleRmNamesTheIdentitiesThatLoseTheirRights(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/ops-backup": `{"data":{"Sys.Audit":1,"Sys.Modify":1}}`,
		"GET /api2/json/access/roles":            `{"data":[{"roleid":"ops-backup","privs":"Sys.Audit,Sys.Modify","special":0}]}`,
		"GET /api2/json/access/acl": `{"data":[{"path":"/","roleid":"ops-backup","type":"token",
			"ugid":"automation@pve!pvectl","propagate":1}]}`,
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "access", "role", "rm", "ops-backup", "--yes")
	if err != nil {
		t.Fatalf("access role rm: %v", err)
	}
	if !strings.Contains(stderr, "1 identité(s)") || !strings.Contains(stderr, "automation@pve!pvectl") {
		t.Errorf("le pre-read doit nommer qui perd ces droits :\n%s", stderr)
	}
	if !strings.Contains(stderr, "VISIBLES") {
		t.Errorf("la liste des ACL est filtrée par le nœud, il faut le dire :\n%s", stderr)
	}
	del := roleWriteOf(t, calls, http.MethodDelete)
	if del.path != "/api2/json/access/roles/ops-backup" {
		t.Errorf("chemin = %q", del.path)
	}
}

// Un --dry-run montre la requête et n'en émet aucune.
func TestRoleAddDryRunWritesNothing(t *testing.T) {
	srv, calls := roleWriteServer(t, map[string]string{
		"GET /api2/json/access/roles/Administrator": adminRolePrivs,
		"GET /api2/json/access/roles/ops-backup":    roleAbsent,
	})
	point(t, srv.URL)

	stdout, stderr, err := run(t, "access", "role", "add", "ops-backup",
		"--privs", "Sys.Modify,Sys.Audit", "--dry-run")
	if err != nil {
		t.Fatalf("access role add --dry-run: %v", err)
	}
	if !strings.Contains(stderr, "POST /access/roles") {
		t.Errorf("le plan doit nommer la requête :\n%s", stderr)
	}
	if !strings.Contains(stderr, "Sys.Audit,Sys.Modify") {
		t.Errorf("le plan doit montrer la liste FINALE, triée :\n%s", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("un --dry-run n'a pas de résultat à rendre sur stdout : %q", stdout)
	}
	noRoleWrite(t, calls)
}
