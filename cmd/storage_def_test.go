package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/testutil"
)

// -------------------------------------------------- harnais (PVX-078)

// storeWrite enregistre le CORPS des requêtes, ce que testutil ne fait pas.
// Les refus et les --dry-run se vérifient sur le plan ; ce qui part vraiment
// sur le réseau, non — et c'est là que vivent les erreurs qui coûtent une
// destination de sauvegarde.
type storeWrite struct {
	method string
	path   string
	form   url.Values
}

// storeAbsent fait répondre comme le nœud sur un stockage inconnu : un HTTP
// 500, pas un 404, avec « storage 'x' does not exist ».
const storeAbsent = "\x00absent"

// storeWriteServer répond aux lectures et retient les écritures.
//
// Il est volontairement à ÉTAT : après un DELETE, le chemin supprimé répond
// 500 comme le nœud. Sans ça, le post-read d'un « rm » verrait le stockage
// répondre encore et la commande ne prouverait rien.
func storeWriteServer(t *testing.T, answers map[string]string) (*httptest.Server, *[]storeWrite) {
	t.Helper()
	var calls []storeWrite
	deleted := map[string]bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls = append(calls, storeWrite{method: r.Method, path: r.URL.Path, form: r.PostForm})
		if r.Method == http.MethodDelete {
			deleted[r.URL.Path] = true
		}
		w.Header().Set("Content-Type", "application/json")

		body, ok := answers[r.Method+" "+r.URL.Path]
		if deleted[r.URL.Path] && r.Method == http.MethodGet {
			body, ok = storeAbsent, true
		}
		if !ok {
			body = `{"data":null}`
		}
		if body == storeAbsent {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"data":null,"message":"storage 'absent' does not exist\n"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func storeWriteOf(t *testing.T, calls *[]storeWrite, method string) storeWrite {
	t.Helper()
	for _, c := range *calls {
		if c.method == method {
			return c
		}
	}
	t.Fatalf("aucune requête %s émise : %+v", method, *calls)
	return storeWrite{}
}

func noStoreWrite(t *testing.T, calls *[]storeWrite) {
	t.Helper()
	for _, c := range *calls {
		if c.method != http.MethodGet {
			t.Fatalf("le refus doit précéder toute écriture : %+v", *calls)
		}
	}
}

// nasDef est une définition NFS telle que GET /storage/{storage} la rend.
const nasDef = `{"data":{"storage":"nas-backup","type":"nfs","content":"backup,iso",
	"server":"192.0.2.50","export":"/export/pve","digest":"921a2c39e40935cc1d681235282a3f4359c66196"}}`

// -------------------------------------------------- refus locaux

// --content est obligatoire ici alors que l'API le donne pour optionnel : sans
// lui, PVE choisit un défaut qui peut très bien ne pas contenir « backup », et
// on obtient un stockage d'apparence normale sur lequel rien n'atterrira.
func TestStorageDefAddDemandsContent(t *testing.T) {
	srv, calls := storeWriteServer(t, nil)
	point(t, srv.URL)

	_, _, err := run(t, "storage", "def", "add", "nas-backup", "--type", "nfs",
		"--server", "192.0.2.50", "--export", "/export/pve", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--content") {
		t.Fatalf("--content doit être exigé : %v", err)
	}
	noStoreWrite(t, calls)
}

// La validation type/drapeaux est LOCALE : un NFS sans export ne doit pas
// coûter un aller-retour pour récolter un 400 moins clair.
func TestStorageDefAddRefusesAnIncompleteTypeWithoutCallingTheNode(t *testing.T) {
	srv, calls := storeWriteServer(t, nil)
	point(t, srv.URL)

	_, _, err := run(t, "storage", "def", "add", "nas-backup", "--type", "nfs",
		"--server", "192.0.2.50", "--content", "backup", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--export") {
		t.Fatalf("un NFS sans --export doit être refusé : %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("aucune requête ne doit partir, pas même une lecture : %+v", *calls)
	}
}

// Un PBS sans mot de passe est accepté par le nœud et ne recevra jamais rien :
// l'échec serait silencieux et différé jusqu'au jour de la restauration.
func TestStorageDefAddRefusesAPBSWithNoPasswordAvailable(t *testing.T) {
	srv, calls := storeWriteServer(t, nil)
	point(t, srv.URL)
	t.Setenv(EnvStoragePassword, "")

	_, _, err := run(t, "storage", "def", "add", "pbs-infra", "--type", "pbs",
		"--server", "pbs.lan", "--datastore", "archives",
		"--username", "archiver@pbs", "--content", "backup", "--yes")
	if err == nil {
		t.Fatal("un PBS sans mot de passe doit être refusé")
	}
	if !strings.Contains(err.Error(), EnvStoragePassword) {
		t.Errorf("le refus doit nommer la variable d'environnement : %v", err)
	}
	noStoreWrite(t, calls)
}

// --guest n'est pas une échappatoire pour un PBS : le partage invité n'existe
// que côté CIFS.
func TestStorageDefAddRefusesGuestOnAPBS(t *testing.T) {
	srv, calls := storeWriteServer(t, nil)
	point(t, srv.URL)

	_, _, err := run(t, "storage", "def", "add", "pbs-infra", "--type", "pbs",
		"--server", "pbs.lan", "--datastore", "archives",
		"--username", "archiver@pbs", "--content", "backup", "--guest", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--guest") {
		t.Fatalf("--guest doit être refusé sur un pbs : %v", err)
	}
	noStoreWrite(t, calls)
}

// Un PBS ne stocke QUE des sauvegardes. Le nœud accepterait « iso » sans rien
// en faire.
func TestStorageDefAddRefusesAContentThePBSWillNeverStore(t *testing.T) {
	srv, calls := storeWriteServer(t, nil)
	point(t, srv.URL)
	t.Setenv(EnvStoragePassword, "s3cr3t")

	_, _, err := run(t, "storage", "def", "add", "pbs-infra", "--type", "pbs",
		"--server", "pbs.lan", "--datastore", "archives",
		"--username", "archiver@pbs", "--content", "backup,iso", "--yes")
	if err == nil || !strings.Contains(err.Error(), "n'accepte pas le contenu") {
		t.Fatalf("un PBS n'accepte que « backup » : %v", err)
	}
	noStoreWrite(t, calls)
}

// LE piège du PUT : export/share/datastore/path/type sont dans le schéma du
// POST et absents de celui du PUT. Le refus doit nommer la sortie — supprimer
// puis recréer — et dire que rien n'est effacé au passage.
func TestStorageDefSetRefusesTheImmutableFieldsAndNamesTheWayOut(t *testing.T) {
	for _, flag := range []string{"export", "share", "datastore", "path"} {
		t.Run(flag, func(t *testing.T) {
			srv, calls := storeWriteServer(t, map[string]string{
				"GET /api2/json/storage/nas-backup": nasDef,
			})
			point(t, srv.URL)

			_, _, err := run(t, "storage", "def", "set", "nas-backup", "--"+flag, "/ailleurs", "--yes")
			if err == nil {
				t.Fatalf("--%s n'est pas modifiable et doit être refusé", flag)
			}
			if !strings.Contains(err.Error(), "storage def rm") ||
				!strings.Contains(err.Error(), "storage def add") {
				t.Errorf("le refus doit nommer l'alternative supprimer/recréer : %v", err)
			}
			if !strings.Contains(err.Error(), "AUCUNE donnée") {
				t.Errorf("le refus doit rassurer sur les données : %v", err)
			}
			if len(*calls) != 0 {
				t.Fatalf("le refus doit précéder toute requête : %+v", *calls)
			}
		})
	}
}

func TestStorageDefSetRefusesANoOp(t *testing.T) {
	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
	})
	point(t, srv.URL)

	_, _, err := run(t, "storage", "def", "set", "nas-backup", "--yes")
	if err == nil || !strings.Contains(err.Error(), "aucune modification") {
		t.Fatalf("un set sans drapeau doit être refusé : %v", err)
	}
	noStoreWrite(t, calls)
}

// -------------------------------------------------- écritures réelles

// LE test de cette famille côté « set » : le PUT est partiel, et le digest lu
// au pre-read repart avec l'écriture. Sans lui, deux administrateurs qui
// éditent en même temps s'écrasent en silence.
func TestStorageDefSetSendsOnlyTheChangedKeysAndTheDigest(t *testing.T) {
	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
	})
	point(t, srv.URL)

	if _, _, err := run(t, "storage", "def", "set", "nas-backup",
		"--content", "backup", "--yes"); err != nil {
		t.Fatalf("storage def set: %v", err)
	}

	put := storeWriteOf(t, calls, http.MethodPut)
	if put.path != "/api2/json/storage/nas-backup" {
		t.Errorf("chemin = %q", put.path)
	}
	if got := put.form.Get("content"); got != "backup" {
		t.Errorf("content = %q", got)
	}
	if got := put.form.Get("digest"); got != "921a2c39e40935cc1d681235282a3f4359c66196" {
		t.Fatalf("digest = %q — la garde anti-écrasement concurrent n'est pas renvoyée", got)
	}
	// Rien d'autre ne voyage. « server » et « username » sont modifiables et
	// n'ont pas été demandés ; « export » et « type » ne sont même pas dans le
	// schéma du PUT.
	for _, unwanted := range []string{"server", "username", "export", "type", "path", "disable", "password"} {
		if _, present := put.form[unwanted]; present {
			t.Errorf("%q n'a pas été demandé et ne doit pas partir : %v", unwanted, put.form)
		}
	}
}

// « --password » est un booléen qui DEMANDE une resaisie. Le lire comme les
// autres drapeaux — présent donc à envoyer — ferait partir « password= » sur un
// « --password=false », c'est-à-dire EFFACER le mot de passe enregistré pour
// avoir demandé le contraire. Le partage cesserait de se monter, et rien dans la
// sortie ne dirait pourquoi.
func TestStorageDefSetNeverBlanksThePasswordOnAFalseFlag(t *testing.T) {
	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
	})
	point(t, srv.URL)

	// Seul drapeau de la commande : il ne demande RIEN, donc il n'y a rien à
	// écrire et la commande doit le dire plutôt que d'inventer une écriture.
	_, _, err := run(t, "storage", "def", "set", "nas-backup", "--password=false", "--yes")
	if err == nil || !strings.Contains(err.Error(), "aucune modification") {
		t.Fatalf("--password=false ne demande aucune modification : %v", err)
	}
	noStoreWrite(t, calls)

	// Et accompagné d'un vrai changement, il ne doit toujours pas emporter la
	// clé « password ».
	srv, calls = storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
	})
	point(t, srv.URL)

	if _, _, err := run(t, "storage", "def", "set", "nas-backup",
		"--password=false", "--content", "backup", "--yes"); err != nil {
		t.Fatalf("storage def set: %v", err)
	}
	put := storeWriteOf(t, calls, http.MethodPut)
	if _, present := put.form["password"]; present {
		t.Fatalf("« password » ne doit pas partir : %v — un password vide EFFACE le secret", put.form)
	}
}

// Retirer « backup » du contenu a la conséquence d'une suppression sans en
// porter le nom : les jobs planifiés qui écrivaient ici échouent à chaque
// passage, et rien ne le dit. « rm » garde déjà contre ça — « set » doit le
// faire aussi, sinon la garde se contourne par le chemin le plus banal.
func TestStorageDefSetWarnsWhenItStopsAcceptingBackups(t *testing.T) {
	const jobs = `{"data":[{"id":"vzdump-nas","storage":"nas-backup","vmid":"220",
		"schedule":"02:30","enabled":1}]}`

	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef, // content = backup,iso
		"GET /api2/json/cluster/backup":     jobs,
	})
	point(t, srv.URL)

	// --yes passe la confirmation, mais l'avertissement doit rester : c'est lui
	// qui nomme ce qui va casser.
	_, stderr, err := run(t, "storage", "def", "set", "nas-backup", "--content", "iso", "--yes")
	if err != nil {
		t.Fatalf("storage def set: %v", err)
	}
	if !strings.Contains(stderr, "vzdump-nas") {
		t.Errorf("le job qui écrit ici doit être nommé :\n%s", stderr)
	}
	if got := storeWriteOf(t, calls, http.MethodPut).form.Get("content"); got != "iso" {
		t.Errorf("content = %q", got)
	}

	// Le même geste qui GARDE « backup » n'a aucune raison d'alerter.
	srv, _ = storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
		"GET /api2/json/cluster/backup":     jobs,
	})
	point(t, srv.URL)

	if _, stderr, err = run(t, "storage", "def", "set", "nas-backup",
		"--content", "iso,backup", "--yes"); err != nil {
		t.Fatalf("storage def set: %v", err)
	}
	if strings.Contains(stderr, "vzdump-nas") {
		t.Errorf("aucune sauvegarde n'est cassée ici, l'avertissement est du bruit :\n%s", stderr)
	}
}

// --disable=false est une VALEUR (réactivation), pas un effacement : elle doit
// partir, sinon « rallumer un stockage » n'écrit rien du tout.
func TestStorageDefSetSendsDisableAsAValue(t *testing.T) {
	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
	})
	point(t, srv.URL)

	if _, _, err := run(t, "storage", "def", "set", "nas-backup", "--disable=false", "--yes"); err != nil {
		t.Fatalf("storage def set: %v", err)
	}
	put := storeWriteOf(t, calls, http.MethodPut)
	if got := put.form.Get("disable"); got != "0" {
		t.Fatalf("disable = %q, attendu « 0 » — sinon la réactivation n'écrit rien", got)
	}
}

// Le mot de passe part dans le corps, et NULLE PART ailleurs : ni dans le plan,
// ni dans la sortie, ni sur stderr.
func TestStorageDefAddNeverPrintsThePassword(t *testing.T) {
	const secret = "motdepasse-tres-reconnaissable"

	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/smb-sauv": storeAbsent,
	})
	point(t, srv.URL)
	t.Setenv(EnvStoragePassword, secret)

	stdout, stderr, err := run(t, "storage", "def", "add", "smb-sauv", "--type", "cifs",
		"--server", "nas.lan", "--share", "sauv", "--username", "sauvegarde",
		"--content", "backup", "--dry-run")
	if err != nil {
		t.Fatalf("storage def add --dry-run: %v", err)
	}
	if strings.Contains(stdout+stderr, secret) {
		t.Fatalf("le mot de passe a fui dans la sortie :\n%s\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "password") || !strings.Contains(stderr, "<redacted>") {
		t.Errorf("le plan doit montrer « password  <redacted> » :\n%s", stderr)
	}
	// Un --dry-run n'écrit rien.
	for _, c := range *calls {
		if c.method != http.MethodGet {
			t.Fatalf("un --dry-run n'écrit pas : %+v", *calls)
		}
	}
}

// Le même contrôle sur le corps RÉELLEMENT émis : le secret doit y être, et le
// plan ne doit pas l'avoir montré.
func TestStorageDefAddSendsThePasswordInTheBodyOnly(t *testing.T) {
	const secret = "motdepasse-tres-reconnaissable"

	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/smb-sauv": storeAbsent,
	})
	point(t, srv.URL)
	t.Setenv(EnvStoragePassword, secret)

	// Le post-read relit le stockage, qui répond « absent » sur ce serveur : la
	// commande échoue APRÈS le POST, ce qui est le bon comportement pour cette
	// fixture. Ce qui compte ici, c'est le corps du POST.
	_, stderr, _ := run(t, "storage", "def", "add", "smb-sauv", "--type", "cifs",
		"--server", "nas.lan", "--share", "sauv", "--username", "sauvegarde",
		"--content", "backup", "--yes")

	post := storeWriteOf(t, calls, http.MethodPost)
	if got := post.form.Get("password"); got != secret {
		t.Fatalf("le mot de passe doit voyager dans le corps, got %q", got)
	}
	if post.form.Get("share") != "sauv" || post.form.Get("type") != "cifs" {
		t.Errorf("payload = %v", post.form)
	}
	if strings.Contains(stderr, secret) {
		t.Fatalf("le mot de passe a fui sur stderr :\n%s", stderr)
	}
}

// --guest monte le partage en invité : aucun mot de passe n'est envoyé, et
// c'est dit.
func TestStorageDefAddGuestSendsNoPassword(t *testing.T) {
	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/smb-iso": storeAbsent,
	})
	point(t, srv.URL)
	t.Setenv(EnvStoragePassword, "ne-doit-pas-etre-lu")

	_, stderr, _ := run(t, "storage", "def", "add", "smb-iso", "--type", "cifs",
		"--server", "nas.lan", "--share", "iso", "--content", "iso", "--guest", "--yes")

	post := storeWriteOf(t, calls, http.MethodPost)
	if _, present := post.form["password"]; present {
		t.Fatalf("--guest ne doit envoyer aucun mot de passe : %v", post.form)
	}
	if got := post.form.Get("username"); got != "guest" {
		t.Errorf("username = %q, attendu « guest »", got)
	}
	if !strings.Contains(stderr, "INVITÉ") {
		t.Errorf("le choix de l'invité doit être annoncé :\n%s", stderr)
	}
}

// -------------------------------------------------- rm

// Supprimer la destination d'un job planifié le fait échouer à chaque
// exécution, en silence. C'est le mode de panne que ce dépôt existe pour rendre
// visible : le pre-read doit NOMMER les jobs concernés.
func TestStorageDefRmNamesTheBackupJobsThatWriteThere(t *testing.T) {
	const jobs = `{"data":[
		{"id":"vzdump-critique","storage":"nas-backup","vmid":"220,221","schedule":"02:30","enabled":1},
		{"id":"vzdump-ailleurs","storage":"local","vmid":"300","schedule":"daily","enabled":1}]}`

	srv, _ := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
		"GET /api2/json/cluster/backup":     jobs,
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "storage", "def", "rm", "nas-backup", "--yes")
	if err != nil {
		t.Fatalf("storage def rm: %v", err)
	}
	if !strings.Contains(stderr, "vzdump-critique") {
		t.Errorf("le job qui écrit sur ce stockage doit être nommé :\n%s", stderr)
	}
	if strings.Contains(stderr, "vzdump-ailleurs") {
		t.Errorf("un job qui écrit ailleurs n'a rien à faire dans l'avertissement :\n%s", stderr)
	}
	// Et l'effet doit dire ce qui NE disparaît PAS.
	if !strings.Contains(stderr, "DONNÉES du partage restent intactes") {
		t.Errorf("l'effet doit nommer ce qui survit :\n%s", stderr)
	}
}

// La vérification croisée exige Sys.Audit sur /. Un 403 ne doit pas bloquer la
// suppression — mais l'absence d'avertissement ne prouve alors rien, et il faut
// le dire.
func TestStorageDefRmSurvivesA403OnTheBackupJobs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api2/json/cluster/backup":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"data":null,"errors":{"path":"Permission check failed"}}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"data":null}`))
		case r.URL.Path == "/api2/json/storage/nas-backup":
			_, _ = w.Write([]byte(nasDef))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	t.Cleanup(srv.Close)
	point(t, srv.URL)

	// Le post-read relit le stockage, qui répond encore sur ce serveur sans
	// état : la commande finit en erreur. Ce que ce test prouve, c'est que le
	// 403 n'a PAS arrêté la commande avant le DELETE, et qu'il a été annoncé.
	_, stderr, _ := run(t, "storage", "def", "rm", "nas-backup", "--yes")

	if !strings.Contains(stderr, "illisible") {
		t.Errorf("l'échec de la vérification doit être annoncé :\n%s", stderr)
	}
	if !strings.Contains(stderr, "PAS la preuve") {
		t.Errorf("il faut dire que l'absence d'avertissement ne prouve rien :\n%s", stderr)
	}
}

func TestStorageDefRmDryRunWritesNothing(t *testing.T) {
	srv, calls := storeWriteServer(t, map[string]string{
		"GET /api2/json/storage/nas-backup": nasDef,
		"GET /api2/json/cluster/backup":     `{"data":[]}`,
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "storage", "def", "rm", "nas-backup", "--dry-run")
	if err != nil {
		t.Fatalf("storage def rm --dry-run: %v", err)
	}
	if !strings.Contains(stderr, "DELETE /storage/nas-backup") {
		t.Errorf("le plan doit nommer la requête :\n%s", stderr)
	}
	for _, c := range *calls {
		if c.method == http.MethodDelete {
			t.Fatalf("un --dry-run ne supprime pas : %+v", *calls)
		}
	}
}

// -------------------------------------------------- ls

// La liste annonce le trou que cette famille existe pour combler : le lab n'a
// que « local » (type dir) et « local-lvm », donc aucune destination de
// sauvegarde hors du disque du nœud.
func TestStorageDefListWarnsWhenNoBackupTargetLivesOffTheNode(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/storage": "storage-defs.json",
	})
	point(t, srv.URL)

	stdout, stderr, err := run(t, "storage", "def", "ls")
	if err != nil {
		t.Fatalf("storage def ls: %v", err)
	}
	if !strings.Contains(stdout, "local-lvm") {
		t.Errorf("la table doit lister les stockages :\n%s", stdout)
	}
	if !strings.Contains(stderr, "AILLEURS que sur le disque du nœud") {
		t.Errorf("l'absence de destination hors-nœud doit être dite :\n%s", stderr)
	}
}

// L'inverse : un PBS déclaré fait taire l'avertissement. Sans ce test, un
// avertissement toujours affiché passerait pour un contrôle qui fonctionne.
func TestStorageDefListStaysSilentWhenABackupTargetExists(t *testing.T) {
	const defs = `{"data":[
		{"storage":"local","type":"dir","content":"backup,iso","path":"/var/lib/vz"},
		{"storage":"pbs-infra","type":"pbs","content":"backup","server":"pbs.lan","datastore":"archives"}]}`

	srv, _ := storeWriteServer(t, map[string]string{"GET /api2/json/storage": defs})
	point(t, srv.URL)

	stdout, stderr, err := run(t, "storage", "def", "ls")
	if err != nil {
		t.Fatalf("storage def ls: %v", err)
	}
	if strings.Contains(stderr, "AILLEURS que sur le disque du nœud") {
		t.Errorf("un PBS déclaré doit faire taire l'avertissement :\n%s", stderr)
	}
	// La colonne CIBLE est celle qui compte : un nom ne dit pas où ça atterrit.
	if !strings.Contains(stdout, "pbs.lan:archives") {
		t.Errorf("la cible doit être affichée :\n%s", stdout)
	}
}

func TestStorageDefShowDecodesTheRealCapture(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/storage/local": "storage-def.json",
	})
	point(t, srv.URL)

	stdout, _, err := run(t, "storage", "def", "show", "local")
	if err != nil {
		t.Fatalf("storage def show: %v", err)
	}
	if !strings.Contains(stdout, "/var/lib/vz") {
		t.Errorf("la sortie doit porter le chemin :\n%s", stdout)
	}
}
