package pve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// La surface HTTP des jobs de sauvegarde planifiés, tenue comme celle du
// firewall : la route et le verbe d'un côté — une faute là ne se voit que
// contre un vrai nœud — le décodage et la composition du payload de l'autre.
//
// Ce qui est spécifiquement à l'épreuve ici, c'est ce qui distingue un job
// d'un vzdump ponctuel : le niveau cluster, la rétention (qui n'a aucun effet
// sans `remove`), et le fait qu'`enabled` absent veuille dire ACTIF.

func jobServer(t *testing.T, body func(r *http.Request) string) (*Client, *[]fwCall) {
	t.Helper()
	return fwServer(t, body)
}

func TestBackupRetentionRendersOnlyPositiveCounters(t *testing.T) {
	// Un compteur à zéro n'est pas « zéro archive gardée », c'est « pas
	// demandé ». L'envoyer quand même ferait supprimer ce qu'on voulait garder.
	r := BackupRetention{Last: 3, Daily: 7}
	if got := r.String(); got != "keep-last=3,keep-daily=7" {
		t.Fatalf("rétention = %q", got)
	}
	if (BackupRetention{}).String() != "" {
		t.Fatal("une rétention vide ne doit rien rendre")
	}
	if !(BackupRetention{}).Empty() {
		t.Fatal("Empty() doit reconnaître la rétention nulle")
	}
	if (BackupRetention{Yearly: 1}).Empty() {
		t.Fatal("un seul compteur suffit à rendre la rétention non vide")
	}

	// L'ordre est fixe : un --dry-run doit être comparable d'une exécution à
	// l'autre, sinon la relecture d'un plan ne prouve rien.
	full := BackupRetention{Last: 1, Hourly: 2, Daily: 3, Weekly: 4, Monthly: 5, Yearly: 6}
	want := "keep-last=1,keep-hourly=2,keep-daily=3,keep-weekly=4,keep-monthly=5,keep-yearly=6"
	if got := full.String(); got != want {
		t.Fatalf("ordre = %q, want %q", got, want)
	}
}

func TestParseBackupRetentionIgnoresKeepAll(t *testing.T) {
	// « keep-all=1 » est le défaut du schéma et veut dire « ne purge rien ».
	// Ce n'est pas un compteur : le lire comme tel afficherait une rétention
	// là où il n'y en a aucune.
	r := ParseBackupRetention("keep-all=1")
	if !r.Empty() {
		t.Fatalf("keep-all ne doit pas être compté : %+v", r)
	}

	r = ParseBackupRetention("keep-last=3,keep-monthly=6")
	if r.Last != 3 || r.Monthly != 6 || r.Daily != 0 {
		t.Fatalf("rétention = %+v", r)
	}

	// Aller-retour : ce que le nœud rend doit se réécrire à l'identique.
	if got := ParseBackupRetention(r.String()).String(); got != r.String() {
		t.Fatalf("aller-retour = %q, want %q", got, r.String())
	}
}

func TestBackupJobOptionsBindRemoveToRetention(t *testing.T) {
	// Le couple qui compte. `remove=1` sans `prune-backups` applique une
	// politique qu'on n'a pas écrite ; `prune-backups` sans `remove` n'a aucun
	// effet. Les deux erreurs sont silencieuses, d'où ce test.
	with := BackupJobOptions{
		Schedule: "02:30", Storage: "pbs", VMIDs: []int{220, 221},
		Mode: ModeSnapshot, Compress: "zstd", Enabled: true,
		Retention: BackupRetention{Last: 3},
	}.Values()
	if with.Get("prune-backups") != "keep-last=3" || with.Get("remove") != "1" {
		t.Fatalf("rétention et remove doivent voyager ensemble : %v", with)
	}

	without := BackupJobOptions{Schedule: "02:30", Storage: "pbs", Enabled: true}.Values()
	if _, present := without["prune-backups"]; present {
		t.Fatalf("aucune rétention ne doit rien envoyer : %v", without)
	}
	if without.Get("remove") != "0" {
		t.Fatalf("sans rétention, remove doit être explicitement 0 : %v", without)
	}
}

func TestBackupJobOptionsRenderOneTargetOnly(t *testing.T) {
	// Les trois cibles sont exclusives côté API, et « all » écrase les autres.
	// Le rendu doit refléter cette exclusivité, pas la laisser au nœud.
	all := BackupJobOptions{All: true, Pool: "lab", VMIDs: []int{220}}.Values()
	if all.Get("all") != "1" {
		t.Fatalf("all = %v", all)
	}
	if all.Get("pool") != "" || all.Get("vmid") != "" {
		t.Fatalf("« all » doit être seul : %v", all)
	}

	pool := BackupJobOptions{Pool: "lab", VMIDs: []int{220}}.Values()
	if pool.Get("pool") != "lab" || pool.Get("vmid") != "" {
		t.Fatalf("« pool » l'emporte sur vmid : %v", pool)
	}

	// La liste de vmid est un CSV, pas un paramètre répété.
	ids := BackupJobOptions{VMIDs: []int{220, 221}}.Values()
	if ids.Get("vmid") != "220,221" {
		t.Fatalf("vmid = %q", ids.Get("vmid"))
	}
	if len(ids["vmid"]) != 1 {
		t.Fatalf("vmid doit être une seule valeur CSV : %v", ids["vmid"])
	}
}

func TestBackupJobOptionsAlwaysStateEnabled(t *testing.T) {
	// enabled=0 doit être ENVOYÉ, pas omis : le schéma vaut 1 par défaut, donc
	// se taire crée un job actif alors qu'on le voulait éteint.
	off := BackupJobOptions{Schedule: "daily", Enabled: false}.Values()
	if off.Get("enabled") != "0" {
		t.Fatalf("enabled = %q, attendu 0", off.Get("enabled"))
	}
	on := BackupJobOptions{Schedule: "daily", Enabled: true}.Values()
	if on.Get("enabled") != "1" {
		t.Fatalf("enabled = %q, attendu 1", on.Get("enabled"))
	}
}

func TestBackupJobOptionsOmitEmptyOptionals(t *testing.T) {
	// Envoyer comment="" n'est pas neutre : PVE l'écrit tel quel et écrase.
	v := BackupJobOptions{Schedule: "daily", Enabled: true}.Values()
	for _, key := range []string{"id", "comment", "node", "notes-template", "mailnotification", "mailto", "storage"} {
		if _, present := v[key]; present {
			t.Errorf("%q vide ne doit pas être envoyé : %v", key, v)
		}
	}
}

func TestBackupJobEnabledDefaultsToTrueWhenAbsent(t *testing.T) {
	// Le piège du schéma : `enabled` absent vaut 1. Un int nu vaudrait 0, et la
	// liste afficherait « désactivé » sur un job qui tourne toutes les nuits.
	c, _ := jobServer(t, func(*http.Request) string {
		return `{"data":[
			{"id":"nocturne","schedule":"02:30","storage":"pbs","vmid":"220,221"},
			{"id":"eteint","schedule":"daily","enabled":0},
			{"id":"actif","schedule":"daily","enabled":1}
		]}`
	})

	jobs, err := c.BackupJobs(context.Background())
	if err != nil {
		t.Fatalf("BackupJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("%d jobs, attendu 3", len(jobs))
	}
	// Tri par identifiant : actif, eteint, nocturne.
	if jobs[0].ID != "actif" || jobs[1].ID != "eteint" || jobs[2].ID != "nocturne" {
		t.Fatalf("tri = %q %q %q", jobs[0].ID, jobs[1].ID, jobs[2].ID)
	}
	if !jobs[0].IsEnabled() {
		t.Error("enabled=1 doit être actif")
	}
	if jobs[1].IsEnabled() {
		t.Error("enabled=0 doit être désactivé")
	}
	if !jobs[2].IsEnabled() {
		t.Error("enabled ABSENT doit être actif — c'est le défaut du schéma")
	}
	if jobs[2].VMID != "220,221" {
		t.Errorf("vmid = %q, attendu la liste CSV telle quelle", jobs[2].VMID)
	}
}

func TestBackupJobTargetNamesTheThreeForms(t *testing.T) {
	if got := (BackupJob{All: 1, Pool: "lab", VMID: "220"}).Target(); got != "tous les guests" {
		t.Errorf("target = %q", got)
	}
	if got := (BackupJob{Pool: "lab", VMID: "220"}).Target(); got != "pool lab" {
		t.Errorf("target = %q", got)
	}
	if got := (BackupJob{VMID: "220,221"}).Target(); got != "220,221" {
		t.Errorf("target = %q", got)
	}
	// Un job sans cible existe : il ne sauvegarde rien, et le dire vaut mieux
	// que d'afficher une colonne vide.
	if got := (BackupJob{}).Target(); got != "—" {
		t.Errorf("target = %q", got)
	}
}

func TestBackupJobsHitsTheClusterEndpoint(t *testing.T) {
	// Cluster, pas nœud. Viser /nodes/{node}/… échouerait en 501 et ferait
	// croire à un problème de droits.
	c, calls := jobServer(t, func(*http.Request) string { return `{"data":[]}` })

	if _, err := c.BackupJobs(context.Background()); err != nil {
		t.Fatalf("BackupJobs: %v", err)
	}
	call := lastCall(t, calls)
	if call.method != http.MethodGet || call.path != "/api2/json/cluster/backup" {
		t.Fatalf("%s %s", call.method, call.path)
	}
}

func TestBackupJobByIDFillsBackTheIdentifier(t *testing.T) {
	// La réponse ne répète pas toujours l'id : l'appelant l'a demandé. Le
	// laisser vide ferait afficher un job anonyme, impossible à modifier.
	c, calls := jobServer(t, func(*http.Request) string {
		return `{"data":{"schedule":"02:30","storage":"pbs","vmid":"220,221",
			"prune-backups":"keep-last=3,keep-daily=7","next-run":1785000000,"mode":"snapshot"}}`
	})

	job, err := c.BackupJobByID(context.Background(), "vzdump-abc")
	if err != nil {
		t.Fatalf("BackupJobByID: %v", err)
	}
	if job.ID != "vzdump-abc" {
		t.Fatalf("id = %q", job.ID)
	}
	if r := job.Retention(); r.Last != 3 || r.Daily != 7 {
		t.Fatalf("rétention = %+v", r)
	}
	if job.NextRun.Int() != 1785000000 {
		t.Fatalf("next-run = %d", job.NextRun.Int())
	}
	if call := lastCall(t, calls); call.path != "/api2/json/cluster/backup/vzdump-abc" {
		t.Fatalf("route = %s", call.path)
	}
}

func TestCreateBackupJobPostsToTheCollection(t *testing.T) {
	c, calls := jobServer(t, func(*http.Request) string { return `{"data":null}` })

	err := c.CreateBackupJob(context.Background(), BackupJobOptions{
		Schedule: "02:30", Storage: "pbs", VMIDs: []int{220, 221},
		Mode: ModeSnapshot, Compress: "zstd", Enabled: true,
		Retention: BackupRetention{Last: 3, Daily: 7},
		Comment:   "guests critiques",
	})
	if err != nil {
		t.Fatalf("CreateBackupJob: %v", err)
	}

	call := lastCall(t, calls)
	if call.method != http.MethodPost || call.path != "/api2/json/cluster/backup" {
		t.Fatalf("%s %s", call.method, call.path)
	}
	for key, want := range map[string]string{
		"schedule": "02:30", "storage": "pbs", "vmid": "220,221",
		"mode": "snapshot", "compress": "zstd", "enabled": "1",
		"prune-backups": "keep-last=3,keep-daily=7", "remove": "1",
		"comment": "guests critiques",
	} {
		if got := call.form.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestUpdateBackupJobIsAPartialPUT(t *testing.T) {
	// PVE distingue POST et PUT ici comme sur les options de firewall, et le
	// PUT est PARTIEL : renvoyer tous les défauts de la CLI remettrait la
	// rétention à zéro sur un job qu'on voulait seulement replanifier.
	c, calls := jobServer(t, func(*http.Request) string { return `{"data":null}` })

	v := url.Values{}
	v.Set("schedule", "04:00")
	if err := c.UpdateBackupJob(context.Background(), "vzdump-abc", v); err != nil {
		t.Fatalf("UpdateBackupJob: %v", err)
	}

	call := lastCall(t, calls)
	if call.method != http.MethodPut || call.path != "/api2/json/cluster/backup/vzdump-abc" {
		t.Fatalf("%s %s", call.method, call.path)
	}
	if call.form.Get("schedule") != "04:00" {
		t.Fatalf("corps = %v", call.form)
	}
	if len(call.form) != 1 {
		t.Fatalf("le PUT doit rester partiel, corps = %v", call.form)
	}
}

func TestDeleteBackupJobCarriesNoBody(t *testing.T) {
	// Un DELETE porteur d'un corps de formulaire earn un 501 du serveur HTTP de
	// PVE, avant même la couche schéma (PVX-031).
	c, calls := jobServer(t, func(*http.Request) string { return `{"data":null}` })

	if err := c.DeleteBackupJob(context.Background(), "vzdump-abc"); err != nil {
		t.Fatalf("DeleteBackupJob: %v", err)
	}
	call := lastCall(t, calls)
	if call.method != http.MethodDelete || call.path != "/api2/json/cluster/backup/vzdump-abc" {
		t.Fatalf("%s %s", call.method, call.path)
	}
	if len(call.form) != 0 {
		t.Fatalf("corps sur un DELETE = %v", call.form)
	}
}

func TestBackupJobIDsAreEscapedInThePath(t *testing.T) {
	// Le paramètre de chemin n'a AUCUN format déclaré (juste string, maxLength
	// 50) : rien côté schéma ne garantit qu'un '/' n'y arrivera pas, et il
	// inventerait alors un segment, donc une autre route que celle visée. Le
	// `pve-configid` du POST est plus strict, mais il ne protège pas ce
	// chemin-ci — c'est l'échappement qui le fait.
	c, calls := jobServer(t, func(*http.Request) string { return `{"data":{}}` })

	if _, err := c.BackupJobByID(context.Background(), "a/b"); err != nil {
		t.Fatalf("BackupJobByID: %v", err)
	}
	if call := lastCall(t, calls); strings.HasSuffix(call.path, "/a/b") {
		t.Fatalf("le '/' n'a pas été échappé : %s", call.path)
	}
}

func TestFormatNextRunTellsTheThreeCases(t *testing.T) {
	// Les trois états d'un job ne se distinguent que par cette colonne.
	if got := FormatNextRun(0, false); got != "désactivé" {
		t.Errorf("job éteint = %q", got)
	}
	if got := FormatNextRun(0, true); !strings.Contains(got, "jamais") {
		t.Errorf("planification non retenue = %q — il faut le DIRE, pas afficher un vide", got)
	}
	if got := FormatNextRun(1785000000, true); !strings.Contains(got, "-") {
		t.Errorf("date attendue, got %q", got)
	}
}

func TestValidateScheduleRefusesTheSilentNoop(t *testing.T) {
	// PVE accepte un job sans schedule. Il ne tourne jamais, et rien ne le dit.
	if err := ValidateSchedule("  "); err == nil {
		t.Fatal("une planification vide doit être refusée avant l'appel")
	}
	if err := ValidateSchedule("mon..fri 22:00"); err != nil {
		t.Fatalf("planification valide refusée : %v", err)
	}
}

func TestBackupJobPathHelpersMatchTheirEndpoints(t *testing.T) {
	// Les helpers servent au --dry-run : s'ils dérivaient de leur endpoint, le
	// plan affiché deviendrait une fiction polie.
	if got := BackupJobsPath(); got != "/cluster/backup" {
		t.Errorf("BackupJobsPath = %q", got)
	}
	if got := BackupJobPath("vzdump-abc"); got != "/cluster/backup/vzdump-abc" {
		t.Errorf("BackupJobPath = %q", got)
	}
}

func TestBackupJobCallsPropagateAPIErrors(t *testing.T) {
	// Un 403 ici veut dire que le token n'a pas Sys.Modify sur « / » — le
	// privilège que /cluster/backup exige et qu'un token de moindre privilège
	// n'a pas. Avaler l'erreur laisserait croire le job planifié.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":{"permissions":"Sys.Modify requis sur /"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{Endpoint: srv.URL, TokenID: "a@pve!t", Secret: "s", Transport: srv.Client().Transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"BackupJobs":      func() error { _, e := c.BackupJobs(ctx); return e },
		"BackupJobByID":   func() error { _, e := c.BackupJobByID(ctx, "x"); return e },
		"CreateBackupJob": func() error { return c.CreateBackupJob(ctx, BackupJobOptions{}) },
		"UpdateBackupJob": func() error { return c.UpdateBackupJob(ctx, "x", url.Values{}) },
		"DeleteBackupJob": func() error { return c.DeleteBackupJob(ctx, "x") },
	} {
		if err := call(); err == nil {
			t.Errorf("%s a avalé le 403", name)
		}
	}
}

func TestRetentionSetWritesOneCounterAtATime(t *testing.T) {
	// La fusion vit là-dessus : partir de la politique du nœud et n'écraser que
	// les compteurs reçus. Un Set qui se tromperait de champ déplacerait
	// silencieusement un palier de rétention.
	r := ParseBackupRetention("keep-last=3,keep-daily=7")
	if !r.Set("keep-last", 5) {
		t.Fatal("keep-last doit être reconnu")
	}
	if got := r.String(); got != "keep-last=5,keep-daily=7" {
		t.Fatalf("fusion = %q — les autres paliers doivent survivre", got)
	}
	for _, key := range RetentionKeys {
		if !(&BackupRetention{}).Set(key, 1) {
			t.Errorf("%q doit être reconnu", key)
		}
	}
	if (&BackupRetention{}).Set("keep-all", 1) {
		t.Error("keep-all n'est pas un compteur : Set doit le refuser")
	}
}

func TestJobPrunesReadsRemoveAndDefaultsToTrue(t *testing.T) {
	// « remove » est l'interrupteur de la rétention. Absent, il vaut 1 comme
	// « enabled » ; à 0, la politique est écrite mais n'agit pas.
	c, _ := jobServer(t, func(*http.Request) string {
		return `{"data":[
			{"id":"defaut","prune-backups":"keep-last=3"},
			{"id":"arme","prune-backups":"keep-last=3","remove":1},
			{"id":"inerte","prune-backups":"keep-last=3","remove":0},
			{"id":"vide"}
		]}`
	})
	jobs, err := c.BackupJobs(context.Background())
	if err != nil {
		t.Fatalf("BackupJobs: %v", err)
	}
	byID := map[string]BackupJob{}
	for _, j := range jobs {
		byID[j.ID] = j
	}

	if !byID["defaut"].Prunes() {
		t.Error("remove ABSENT doit valoir 1 — c'est le défaut du schéma")
	}
	if !byID["arme"].Prunes() || byID["inerte"].Prunes() {
		t.Error("remove doit être lu tel quel quand il est présent")
	}

	// Et le rendu doit DIRE l'inertie, pas se contenter d'afficher la politique.
	if got := byID["arme"].RetentionSummary(); got != "keep-last=3" {
		t.Errorf("résumé armé = %q", got)
	}
	if got := byID["inerte"].RetentionSummary(); !strings.Contains(got, "INERTE") {
		t.Errorf("résumé inerte = %q — une politique désarmée qui s'affiche normalement rassure à tort", got)
	}
	if got := byID["vide"].RetentionSummary(); got != "" {
		t.Errorf("sans politique, le résumé doit être vide, got %q", got)
	}
}

func TestValidateJobIDRejectsOnlyWhatCannotPass(t *testing.T) {
	// Volontairement minimal : refuser ici un id que le nœud accepterait serait
	// pire que le 400 qu'on cherche à éviter.
	for _, bad := range []string{"", "mon job", "a/b", "x\ty"} {
		if err := ValidateJobID(bad); err == nil {
			t.Errorf("%q doit être refusé", bad)
		}
	}
	for _, ok := range []string{"vzdump-critique", "backup_nuit", "b7ab3e8a-5e8f-4f3c"} {
		if err := ValidateJobID(ok); err != nil {
			t.Errorf("%q doit passer : %v", ok, err)
		}
	}
}
