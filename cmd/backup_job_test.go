package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/testutil"
)

// Les jobs planifiés, vus depuis la CLI.
//
// Note de provenance : testdata/backup-job{,s}.json ne sont PAS des captures du
// lab — le secret du token n'était pas disponible au moment de l'écriture. Ils
// sont dérivés du schéma de l'API viewer PVE 9.x (search-pve-api.ts
// "/cluster/backup"), champ par champ. C'est une fixture plus faible qu'une
// capture, et ça se dit : la première capture réelle doit les remplacer.

func TestBackupJobListShowsWhatALineOfNamesWouldHide(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup": "backup-jobs.json",
	})
	point(t, srv.URL)

	stdout, _, err := run(t, "backup", "job", "ls")
	if err != nil {
		t.Fatalf("backup job ls: %v", err)
	}
	// La rétention et la prochaine exécution sont les deux colonnes qui
	// distinguent un job vivant d'un job qui ne sauvegardera jamais rien.
	if !strings.Contains(stdout, "keep-last=3,keep-daily=7") {
		t.Errorf("la rétention doit être lisible :\n%s", stdout)
	}
	if !strings.Contains(stdout, "220,221") {
		t.Errorf("la cible doit être lisible :\n%s", stdout)
	}
	// Le second job du fixture n'a aucune rétention : la colonne doit le dire.
	if !strings.Contains(stdout, "vzdump-sans-retention") {
		t.Errorf("tous les jobs doivent apparaître :\n%s", stdout)
	}
}

func TestBackupJobListSaysWhenThereIsNone(t *testing.T) {
	// Zéro job n'est pas un tableau vide anodin : c'est un RPO infini pour tout
	// le cluster. Le silence serait la pire des sorties.
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup": "qemu-empty.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "backup", "job", "ls")
	if err != nil {
		t.Fatalf("backup job ls: %v", err)
	}
	if !strings.Contains(stderr, "INFINI") {
		t.Errorf("l'absence de job doit être nommée :\n%s", stderr)
	}
}

func TestBackupJobShowRendersTheRetention(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup/vzdump-critique": "backup-job.json",
	})
	point(t, srv.URL)

	stdout, stderr, err := run(t, "backup", "job", "show", "vzdump-critique")
	if err != nil {
		t.Fatalf("backup job show: %v", err)
	}
	if !strings.Contains(stdout, "keep-last=3,keep-daily=7") {
		t.Errorf("la rétention doit figurer entière :\n%s", stdout)
	}
	// Ce job purge bel et bien : aucun avertissement ne doit être émis, sinon
	// l'avertissement perd tout pouvoir de signal.
	if strings.Contains(stderr, "⚠") {
		t.Errorf("un job sain ne doit rien déclencher :\n%s", stderr)
	}
}

func TestBackupJobShowWarnsWhenNothingPrunes(t *testing.T) {
	// Le job n'a aucune rétention : le stockage se remplit sans fin.
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup/vzdump-sans-retention": "backup-job-sans-retention.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "backup", "job", "show", "vzdump-sans-retention")
	if err != nil {
		t.Fatalf("backup job show: %v", err)
	}
	if !strings.Contains(stderr, "aucune rétention") {
		t.Errorf("l'absence de rétention doit être dite :\n%s", stderr)
	}
}

func TestBackupJobShowWarnsOnARetentionDisarmedByRemoveZero(t *testing.T) {
	// Le pire des deux mondes : une politique écrite, donc rassurante, et un
	// remove=0 qui la désarme. Sans lecture de « remove », la CLI afficherait
	// « keep-last=3,keep-daily=7 » sur un job qui ne purge rien.
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup/vzdump-inerte": "backup-job-inerte.json",
	})
	point(t, srv.URL)

	stdout, stderr, err := run(t, "backup", "job", "show", "vzdump-inerte")
	if err != nil {
		t.Fatalf("backup job show: %v", err)
	}
	if !strings.Contains(stdout, "INERTE") {
		t.Errorf("la rétention désarmée doit être signalée dans la valeur même :\n%s", stdout)
	}
	if !strings.Contains(stderr, "remove=0") {
		t.Errorf("l'avertissement doit nommer la cause :\n%s", stderr)
	}
}

func TestBackupJobCreateRefusesAJobThatNeverPrunes(t *testing.T) {
	// Le garde-fou central de cette famille : un job sans rétention finit par
	// saturer le stockage, c'est-à-dire par causer la panne que la sauvegarde
	// existait pour absorber. Le refus doit précéder toute requête.
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "create",
		"--vmid", "220,221", "--storage", "pbs-infra", "--schedule", "02:30", "--yes")
	if err == nil {
		t.Fatal("un job sans rétention doit être refusé")
	}
	if !strings.Contains(err.Error(), "--keep-last") {
		t.Errorf("le refus doit donner la sortie : %v", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir : %v", srv.Requests)
	}
}

func TestBackupJobCreateRefusesAnAmbiguousTarget(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "create",
		"--vmid", "220", "--all", "--storage", "pbs", "--schedule", "daily", "--keep-last", "3", "--yes")
	if err == nil || !strings.Contains(err.Error(), "s'excluent") {
		t.Fatalf("--vmid avec --all doit être refusé : %v", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir : %v", srv.Requests)
	}
}

func TestBackupJobCreateRefusesAnEmptySchedule(t *testing.T) {
	// PVE accepterait le job. Il ne tournerait jamais, sans rien dire.
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "create",
		"--vmid", "220", "--storage", "pbs", "--keep-last", "3", "--yes")
	if err == nil || !strings.Contains(err.Error(), "JAMAIS") {
		t.Fatalf("une planification vide doit être refusée : %v", err)
	}
}

func TestBackupJobCreateDryRunShowsThePayloadItWouldSend(t *testing.T) {
	// Le --dry-run est le meilleur outil pédagogique de la CLI : il doit
	// montrer la requête réelle, pas une paraphrase.
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup": "backup-jobs.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "backup", "job", "create",
		"--vmid", "220,221", "--storage", "pbs-infra", "--schedule", "02:30",
		"--keep-last", "3", "--keep-daily", "7", "--dry-run")
	if err != nil {
		t.Fatalf("backup job create --dry-run: %v", err)
	}
	// On épingle les LIGNES du payload, pas des sous-chaînes : « keep-last=3,
	// keep-daily=7 » apparaît aussi dans la ligne « effet », donc une simple
	// recherche de sous-chaîne serait satisfaite par la paraphrase et ne
	// prouverait rien du paramètre réellement envoyé.
	for _, want := range []string{
		"POST /cluster/backup",
		"prune-backups    keep-last=3,keep-daily=7",
		"remove           1",
		"vmid             220,221",
		"--dry-run : aucune requête d'écriture émise.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("le plan doit contenir %q :\n%s", want, stderr)
		}
	}
	for _, req := range srv.Requests {
		if strings.HasPrefix(req, "POST ") {
			t.Fatalf("un --dry-run n'écrit pas : %v", srv.Requests)
		}
	}
}

func TestBackupJobCreateRefusesAnIDAlreadyTaken(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup": "backup-jobs.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "create", "--id", "vzdump-critique",
		"--vmid", "220", "--storage", "pbs", "--schedule", "daily", "--keep-last", "3", "--yes")
	if err == nil || !strings.Contains(err.Error(), "backup job set") {
		t.Fatalf("un id déjà pris doit renvoyer vers la modification : %v", err)
	}
}

func TestBackupJobSetSendsOnlyWhatChanged(t *testing.T) {
	// Le point qui évite la catastrophe silencieuse : un PUT qui renverrait
	// tous les défauts de la CLI remettrait compress à zstd et effacerait la
	// rétention d'un job qu'on voulait seulement replanifier.
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup/vzdump-critique": "backup-job.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "backup", "job", "set", "vzdump-critique", "--schedule", "04:00", "--dry-run")
	if err != nil {
		t.Fatalf("backup job set --dry-run: %v", err)
	}
	if !strings.Contains(stderr, "PUT /cluster/backup/vzdump-critique") {
		t.Errorf("le verbe et la route doivent figurer :\n%s", stderr)
	}
	if !strings.Contains(stderr, "schedule") || !strings.Contains(stderr, "04:00") {
		t.Errorf("la modification demandée doit figurer :\n%s", stderr)
	}
	for _, unwanted := range []string{"compress", "prune-backups", "enabled", "mode"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("%q n'a pas été demandé et ne doit pas partir :\n%s", unwanted, stderr)
		}
	}
}

func TestBackupJobSetRefusesAnEmptyChange(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "set", "vzdump-critique", "--yes")
	if err == nil || !strings.Contains(err.Error(), "aucune modification") {
		t.Fatalf("un PUT sans changement doit être refusé : %v", err)
	}
	// Et refusé AVANT toute requête : lire le job pour ne rien lui écrire
	// serait un aller-retour gratuit.
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir : %v", srv.Requests)
	}
}

func TestBackupJobSetRefusesToLeaveNothingPruning(t *testing.T) {
	// --keep-last 0 sur un job dont c'est le seul compteur laisserait une
	// politique vide : plus rien ne purge, le stockage se remplit.
	const job = `{"data":{"id":"j","schedule":"daily","storage":"local","vmid":"220",
		"enabled":1,"prune-backups":"keep-last=3","remove":1}}`

	srv, calls := jobWriteServer(t, map[string]string{"GET /api2/json/cluster/backup/j": job})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "set", "j", "--keep-last", "0", "--yes")
	if err == nil || !strings.Contains(err.Error(), "AUCUNE purge") {
		t.Fatalf("une rétention nulle doit être refusée : %v", err)
	}
	for _, c := range *calls {
		if c.method == http.MethodPut {
			t.Fatalf("le refus doit précéder l'écriture : %+v", *calls)
		}
	}
}

// À l'inverse, mettre un compteur à zéro alors qu'un AUTRE reste actif est une
// demande légitime : on retire un palier, on n'éteint pas la purge.
func TestBackupJobSetAllowsDroppingOneTierAmongSeveral(t *testing.T) {
	const job = `{"data":{"id":"j","schedule":"daily","storage":"local","vmid":"220",
		"enabled":1,"prune-backups":"keep-last=3,keep-daily=7","remove":1}}`

	srv, calls := jobWriteServer(t, map[string]string{"GET /api2/json/cluster/backup/j": job})
	point(t, srv.URL)

	if _, _, err := run(t, "backup", "job", "set", "j", "--keep-daily", "0", "--yes"); err != nil {
		t.Fatalf("backup job set: %v", err)
	}
	if got := writeOf(t, calls, http.MethodPut).form.Get("prune-backups"); got != "keep-last=3" {
		t.Fatalf("prune-backups = %q", got)
	}
}

func TestBackupJobRmIsDestructiveAndDryRunnable(t *testing.T) {
	// La suppression passe par la confirmation renforcée (retaper l'id) ; le
	// --dry-run doit pouvoir montrer la requête sans rien détruire.
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup/vzdump-critique": "backup-job.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "backup", "job", "rm", "vzdump-critique", "--dry-run")
	if err != nil {
		t.Fatalf("backup job rm --dry-run: %v", err)
	}
	if !strings.Contains(stderr, "DELETE /cluster/backup/vzdump-critique") {
		t.Errorf("le plan doit nommer la requête :\n%s", stderr)
	}
	// L'effet doit dire ce qui NE disparaît PAS : les archives restent, et
	// plus rien ne les purgera.
	if !strings.Contains(stderr, "archives existantes restent") {
		t.Errorf("l'effet doit nommer ce qui survit :\n%s", stderr)
	}
	for _, req := range srv.Requests {
		if strings.HasPrefix(req, "DELETE ") {
			t.Fatalf("un --dry-run ne supprime pas : %v", srv.Requests)
		}
	}
}

// « update » est accepté en alias de « set » : les deux mots désignent la même
// opération, et refuser le second ferait perdre du temps sans rien apprendre.
func TestBackupJobUpdateIsAnAliasOfSet(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/backup/vzdump-critique": "backup-job.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "backup", "job", "update", "vzdump-critique", "--enabled=false", "--dry-run")
	if err != nil {
		t.Fatalf("backup job update: %v", err)
	}
	if !strings.Contains(stderr, "enabled") {
		t.Errorf("le plan doit porter enabled :\n%s", stderr)
	}
}

// ------------------------------------------------------- écritures réelles

// jobWriteServer enregistre le CORPS des requêtes, ce que testutil ne fait pas.
// Les refus et les --dry-run se vérifient sur le plan ; ce qui part vraiment
// sur le réseau, non — et c'est là que vivent les erreurs qui coûtent des
// archives.
type jobWrite struct {
	method string
	path   string
	form   url.Values
}

func jobWriteServer(t *testing.T, answers map[string]string) (*httptest.Server, *[]jobWrite) {
	t.Helper()
	var calls []jobWrite

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls = append(calls, jobWrite{method: r.Method, path: r.URL.Path, form: r.PostForm})
		body, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			body = `{"data":null}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func writeOf(t *testing.T, calls *[]jobWrite, method string) jobWrite {
	t.Helper()
	for _, c := range *calls {
		if c.method == method {
			return c
		}
	}
	t.Fatalf("aucune requête %s émise : %+v", method, *calls)
	return jobWrite{}
}

// LE test de cette famille. « prune-backups » est UNE valeur côté API : envoyer
// le seul compteur qu'on vient de changer efface les autres, et la prochaine
// exécution supprime des archives que personne n'avait demandé de supprimer.
func TestBackupJobSetMergesRetentionInsteadOfReplacingIt(t *testing.T) {
	const job = `{"data":{"id":"vzdump-critique","schedule":"02:30","storage":"pbs-infra",
		"vmid":"220,221","enabled":1,"prune-backups":"keep-last=3,keep-daily=7","remove":1}}`

	srv, calls := jobWriteServer(t, map[string]string{
		"GET /api2/json/cluster/backup/vzdump-critique": job,
	})
	point(t, srv.URL)

	if _, _, err := run(t, "backup", "job", "set", "vzdump-critique", "--keep-last", "5", "--yes"); err != nil {
		t.Fatalf("backup job set: %v", err)
	}

	put := writeOf(t, calls, http.MethodPut)
	got := put.form.Get("prune-backups")
	if got != "keep-last=5,keep-daily=7" {
		t.Fatalf("prune-backups = %q — keep-daily=7 a été effacé, ce qui SUPPRIME des archives", got)
	}
	// Rien d'autre ne doit voyager : le PUT reste partiel sur les autres champs.
	for _, unwanted := range []string{"compress", "mode", "schedule", "enabled", "vmid"} {
		if _, present := put.form[unwanted]; present {
			t.Errorf("%q n'a pas été demandé et ne doit pas partir : %v", unwanted, put.form)
		}
	}
}

// Un job dont la purge est désarmée ne doit pas la voir se rallumer au détour
// d'un --keep-*, ni rester désarmée sans qu'on le dise.
func TestBackupJobSetRefusesToChangeAnInertRetentionSilently(t *testing.T) {
	const inert = `{"data":{"id":"vzdump-inerte","schedule":"daily","storage":"local",
		"vmid":"220","enabled":1,"prune-backups":"keep-last=3","remove":0}}`

	srv, calls := jobWriteServer(t, map[string]string{
		"GET /api2/json/cluster/backup/vzdump-inerte": inert,
	})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "set", "vzdump-inerte", "--keep-last", "5", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--prune") {
		t.Fatalf("changer une rétention inerte doit exiger --prune : %v", err)
	}
	for _, c := range *calls {
		if c.method == http.MethodPut {
			t.Fatalf("le refus doit précéder l'écriture : %+v", *calls)
		}
	}
}

// Vider un champ passe par « delete », pas par une valeur vide : PVE refuse
// all= et node= avec un 400 que rien dans la sortie n'expliquerait.
func TestBackupJobSetClearsFieldsWithTheDeleteParameter(t *testing.T) {
	const job = `{"data":{"id":"j","schedule":"daily","storage":"local","all":1,
		"node":"pve","enabled":1,"prune-backups":"keep-last=3","remove":1}}`

	srv, calls := jobWriteServer(t, map[string]string{"GET /api2/json/cluster/backup/j": job})
	point(t, srv.URL)

	if _, _, err := run(t, "backup", "job", "set", "j", "--all=false", "--run-on", "", "--yes"); err != nil {
		t.Fatalf("backup job set: %v", err)
	}

	put := writeOf(t, calls, http.MethodPut)
	if got := put.form.Get("delete"); got != "all,node" {
		t.Fatalf("delete = %q, attendu « all,node »", got)
	}
	// Surtout : aucune valeur vide ne doit partir sur un champ typé.
	for _, key := range []string{"all", "node"} {
		if _, present := put.form[key]; present {
			t.Errorf("%q ne doit pas être envoyé vide (le nœud rend un 400) : %v", key, put.form)
		}
	}
}

func TestBackupJobSetChangesTheTarget(t *testing.T) {
	// Le nœud efface lui-même les deux autres clés de cible : il suffit
	// d'envoyer celle qu'on veut.
	const job = `{"data":{"id":"j","schedule":"daily","storage":"local","vmid":"220","enabled":1}}`

	srv, calls := jobWriteServer(t, map[string]string{"GET /api2/json/cluster/backup/j": job})
	point(t, srv.URL)

	if _, _, err := run(t, "backup", "job", "set", "j", "--all", "--yes"); err != nil {
		t.Fatalf("backup job set --all: %v", err)
	}
	put := writeOf(t, calls, http.MethodPut)
	if put.form.Get("all") != "1" {
		t.Fatalf("all = %q", put.form.Get("all"))
	}
}

func TestBackupJobCreateNamesTheGeneratedID(t *testing.T) {
	// PVE génère l'identifiant et ne le rend pas : la seule façon honnête de
	// savoir lequel vient d'être créé est de comparer les listes.
	before := `{"data":[{"id":"vzdump-ancien","schedule":"daily","enabled":1}]}`
	after := `{"data":[{"id":"vzdump-ancien","schedule":"daily","enabled":1},
		{"id":"vzdump-genere","schedule":"02:30","storage":"pbs","vmid":"220,221",
		 "enabled":1,"prune-backups":"keep-last=3","remove":1,"next-run":1785000000}]}`

	var seen int
	var calls []jobWrite
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls = append(calls, jobWrite{method: r.Method, path: r.URL.Path, form: r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			seen++
			if seen == 1 {
				_, _ = w.Write([]byte(before))
				return
			}
			_, _ = w.Write([]byte(after))
			return
		}
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()
	point(t, srv.URL)

	stdout, _, err := run(t, "backup", "job", "create",
		"--vmid", "220,221", "--storage", "pbs", "--schedule", "02:30", "--keep-last", "3", "--yes")
	if err != nil {
		t.Fatalf("backup job create: %v", err)
	}
	if !strings.Contains(stdout, "vzdump-genere") {
		t.Errorf("l'identifiant généré doit être nommé :\n%s", stdout)
	}
	post := writeOf(t, &calls, http.MethodPost)
	if post.form.Get("prune-backups") != "keep-last=3" || post.form.Get("remove") != "1" {
		t.Errorf("payload de création = %v", post.form)
	}
}

func TestBackupJobRmVerifiesTheJobIsGone(t *testing.T) {
	// La preuve d'une suppression n'est pas le 200 du DELETE : c'est l'absence
	// du job à la relecture.
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/vzdump-zombie"):
			_, _ = w.Write([]byte(`{"data":{"id":"vzdump-zombie","schedule":"daily","enabled":1}}`))
		case r.Method == http.MethodGet:
			seen++
			// Le nœud continue de lister le job : la suppression n'a pas pris.
			_, _ = w.Write([]byte(`{"data":[{"id":"vzdump-zombie","schedule":"daily","enabled":1}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()
	point(t, srv.URL)

	_, _, err := run(t, "backup", "job", "rm", "vzdump-zombie", "--yes")
	if err == nil || !strings.Contains(err.Error(), "toujours dans") {
		t.Fatalf("un job encore présent après le DELETE doit faire échouer la commande : %v", err)
	}
}
