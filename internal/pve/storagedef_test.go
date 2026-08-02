package pve

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Le décodage des deux captures réelles du nœud. Une fixture écrite à la main
// serait d'accord avec ce que le développeur croyait du schéma — c'est
// exactement le mode d'échec que ce dépôt existe pour éviter.
func TestStorageDefsDecodeTheRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/storage": "storage-defs.json"})

	defs, err := c.StorageDefs(context.Background())
	if err != nil {
		t.Fatalf("StorageDefs: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("%d définitions, want 2", len(defs))
	}
	// Le tri est par identifiant : local avant local-lvm.
	if defs[0].Storage != "local" || defs[1].Storage != "local-lvm" {
		t.Fatalf("tri = %q, %q", defs[0].Storage, defs[1].Storage)
	}
	if defs[0].Type != "dir" || defs[0].Path != "/var/lib/vz" {
		t.Errorf("local = %+v", defs[0])
	}
	if !defs[0].Accepts("backup") {
		t.Errorf("local accepte « backup » dans la capture : %q", defs[0].Content)
	}
	// « disable » est absent des deux entrées : absent veut dire ACTIVÉ.
	if !defs[0].IsEnabled() {
		t.Error("un « disable » absent veut dire activé, pas désactivé")
	}
	// Le digest couvre TOUT storage.cfg : les deux entrées portent le même.
	if defs[0].Digest == "" || defs[0].Digest != defs[1].Digest {
		t.Errorf("digest = %q / %q — il couvre le fichier entier, pas l'entrée",
			defs[0].Digest, defs[1].Digest)
	}
	// Aucun des deux stockages du lab n'est une destination de sauvegarde hors
	// du disque du nœud. C'est le trou que « storage def » existe pour combler.
	for _, d := range defs {
		if d.IsOffNodeBackupTarget() {
			t.Errorf("%q n'est pas une destination hors-nœud : %+v", d.Storage, d)
		}
	}
}

func TestStorageDefByIDDecodesTheRealAnswer(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/storage/local": "storage-def.json"})

	def, err := c.StorageDefByID(context.Background(), "local")
	if err != nil {
		t.Fatalf("StorageDefByID: %v", err)
	}
	if def.Storage != "local" {
		t.Errorf("storage = %q", def.Storage)
	}
	if def.Target() != "/var/lib/vz" {
		t.Errorf("cible = %q", def.Target())
	}
}

// LE piège du champ « content » : le MÊME stockage, lu à une seconde
// d'intervalle, rend « backup,import,vztmpl,iso » par l'index et
// « import,backup,vztmpl,iso » par le détail. Les deux captures le prouvent.
// Toute comparaison octet à octet conclurait à un changement inexistant.
func TestContentComparisonIgnoresOrder(t *testing.T) {
	const fromIndex = "backup,import,vztmpl,iso"
	const fromDetail = "import,backup,vztmpl,iso"

	if fromIndex == fromDetail {
		t.Fatal("les deux captures doivent différer par l'ordre — sinon ce test ne prouve rien")
	}
	if !SameContentTypes(fromIndex, fromDetail) {
		t.Error("l'ordre de « content » n'est pas stable : la comparaison doit porter sur des ENSEMBLES")
	}
	if SameContentTypes("backup,iso", "backup") {
		t.Error("deux ensembles de tailles différentes ne sont pas égaux")
	}
	if SameContentTypes("backup,iso", "backup,vztmpl") {
		t.Error("deux ensembles distincts de même taille ne sont pas égaux")
	}
	// Les espaces et la casse ne font pas partie de l'information.
	if !SameContentTypes(" Backup , ISO ", "iso,backup") {
		t.Error("espaces et casse ne doivent pas décider d'une égalité")
	}
	if !SameContentTypes("", "") {
		t.Error("deux déclarations vides sont égales")
	}
}

func TestValidateStorageDefOptionsRefusesWhatTheNodeWouldAcceptSilently(t *testing.T) {
	for name, tc := range map[string]struct {
		opts StorageDefOptions
		want string
	}{
		"type inconnu": {
			opts: StorageDefOptions{Storage: "s", Type: "ceph", Content: "backup"},
			want: "inconnu de pvecli",
		},
		"nfs sans export": {
			opts: StorageDefOptions{Storage: "s", Type: "nfs", Server: "nas", Content: "backup"},
			want: "--export",
		},
		"cifs sans share": {
			opts: StorageDefOptions{Storage: "s", Type: "cifs", Server: "nas", Content: "iso"},
			want: "--share",
		},
		"pbs sans username": {
			opts: StorageDefOptions{Storage: "s", Type: "pbs", Server: "pbs", Datastore: "arch", Content: "backup"},
			want: "--username",
		},
		"dir sans path": {
			opts: StorageDefOptions{Storage: "s", Type: "dir", Content: "backup"},
			want: "--path",
		},
		"content absent": {
			opts: StorageDefOptions{Storage: "s", Type: "dir", Path: "/srv"},
			want: "--content est obligatoire",
		},
		// Le cas qui coûte le plus cher : PVE ACCEPTE un PBS déclaré « iso » et
		// n'y écrira jamais rien. Aucun 400 ne prévient.
		"pbs avec un contenu qu'il ne stocke pas": {
			opts: StorageDefOptions{
				Storage: "s", Type: "pbs", Server: "pbs", Datastore: "arch",
				Username: "archiver@pbs", Content: "backup,iso",
			},
			want: "n'accepte pas le contenu",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateStorageDefOptions(tc.opts)
			if err == nil {
				t.Fatalf("attendu un refus pour %+v", tc.opts)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("le refus doit nommer %q : %v", tc.want, err)
			}
		})
	}

	ok := StorageDefOptions{
		Storage: "nas-backup", Type: "nfs", Server: "192.0.2.50",
		Export: "/export/pve", Content: "backup",
	}
	if err := ValidateStorageDefOptions(ok); err != nil {
		t.Errorf("une déclaration NFS complète doit passer : %v", err)
	}
}

func TestStorageTypeNeedsPassword(t *testing.T) {
	for typ, wantRequired := range map[string]bool{"pbs": true, "cifs": false} {
		required, accepted := StorageTypeNeedsPassword(typ)
		if !accepted {
			t.Errorf("%s accepte un mot de passe", typ)
		}
		if required != wantRequired {
			t.Errorf("%s : required = %v, want %v", typ, required, wantRequired)
		}
	}
	// Un NFS n'a pas de mot de passe du tout : en demander un serait une
	// invite à saisir un secret que rien ne consommerait.
	if _, accepted := StorageTypeNeedsPassword("nfs"); accepted {
		t.Error("nfs n'accepte pas de mot de passe")
	}
	if _, accepted := StorageTypeNeedsPassword("inconnu"); accepted {
		t.Error("un type inconnu n'accepte rien")
	}
	if StorageTypeLabel("pbs") == "pbs" {
		t.Error("StorageTypeLabel doit rendre le type en clair")
	}
	if StorageTypeLabel("ceph") != "ceph" {
		t.Error("un type inconnu se rend tel quel")
	}
	if len(KnownStorageTypes()) != 4 {
		t.Errorf("types connus = %v", KnownStorageTypes())
	}
}

func TestStorageDefValuesRenderThePost(t *testing.T) {
	o := StorageDefOptions{
		Storage: "pbs-infra", Type: "pbs", Content: "backup",
		Server: "pbs.lan", Datastore: "archives", Username: "archiver@pbs",
		Password: "s3cr3t", Fingerprint: "AA:BB", Port: 8007,
		PruneBackups: "keep-last=3",
	}
	v := o.Values()
	for key, want := range map[string]string{
		"storage": "pbs-infra", "type": "pbs", "content": "backup",
		"server": "pbs.lan", "datastore": "archives", "username": "archiver@pbs",
		"password": "s3cr3t", "fingerprint": "AA:BB", "port": "8007",
		"prune-backups": "keep-last=3",
	} {
		if got := v.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// « disable » absent laisse le défaut du nœud, qui est « activé ». L'envoyer
	// à 0 reviendrait à écrire une valeur que personne n'a demandée.
	if _, present := v["disable"]; present {
		t.Errorf("disable ne doit pas partir quand il n'est pas demandé : %v", v)
	}
	// Le port à 0 n'est pas un port : c'est l'absence de port.
	if got := (StorageDefOptions{Storage: "s", Type: "dir", Path: "/srv"}).Values().Get("port"); got != "" {
		t.Errorf("port = %q sur une option non renseignée", got)
	}
}

func TestStorageDefUpdateValuesStayPartialAndCarryTheDigest(t *testing.T) {
	o := StorageDefOptions{
		Storage: "nas", Content: "backup,iso", Server: "autre.lan",
		Export: "/ailleurs", Disable: true,
	}
	v := o.UpdateValues(map[string]bool{"content": true, "disable": true}, "921a2c39")

	if v.Get("content") != "backup,iso" || v.Get("disable") != "1" {
		t.Errorf("payload = %v", v)
	}
	if v.Get("digest") != "921a2c39" {
		t.Errorf("le digest lu au pre-read doit repartir avec l'écriture : %v", v)
	}
	// Rien d'autre ne voyage : le PUT est partiel. Et « export » n'existe même
	// pas dans le schéma du PUT — l'y voir serait un 400 garanti.
	for _, unwanted := range []string{"server", "export", "path", "type", "username"} {
		if _, present := v[unwanted]; present {
			t.Errorf("%q n'a pas été demandé et ne doit pas partir : %v", unwanted, v)
		}
	}
	// « disable=false » est une VALEUR (réactivation), pas un effacement.
	off := (StorageDefOptions{}).UpdateValues(map[string]bool{"disable": true}, "")
	if off.Get("disable") != "0" {
		t.Errorf("--disable=false doit envoyer 0, pas rien : %v", off)
	}
	if _, present := off["digest"]; present {
		t.Errorf("pas de digest lu, pas de digest envoyé : %v", off)
	}
}

// Les quatre champs immuables doivent rester nommés au même endroit que le
// reste : une liste qui dériverait du schéma ferait refuser un champ modifiable
// ou laisserait passer un 400.
func TestStorageDefKeyListsDoNotOverlap(t *testing.T) {
	updatable := map[string]bool{}
	for _, k := range StorageDefUpdatableKeys {
		updatable[k] = true
	}
	for _, k := range StorageDefPostOnlyKeys {
		if updatable[k] {
			t.Errorf("%q est déclaré à la fois modifiable et POST-only", k)
		}
	}
}

func TestStorageDefWritesUseTheRightVerbs(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		c, s := newSpy(t, `{"data":{"storage":"nas","type":"nfs","config":{}}}`)
		err := c.CreateStorageDef(context.Background(), StorageDefOptions{
			Storage: "nas", Type: "nfs", Server: "nas.lan", Export: "/export", Content: "backup",
		})
		if err != nil {
			t.Fatalf("CreateStorageDef: %v", err)
		}
		if s.method != http.MethodPost || s.path != "/api2/json/storage" {
			t.Errorf("%s %s", s.method, s.path)
		}
		if s.body.Get("storage") != "nas" || s.body.Get("export") != "/export" {
			t.Errorf("payload = %v", s.body)
		}
	})

	t.Run("update", func(t *testing.T) {
		c, s := newSpy(t, `{"data":{"storage":"nas","type":"nfs","config":{}}}`)
		v := (StorageDefOptions{Content: "backup"}).UpdateValues(map[string]bool{"content": true}, "d1")
		if err := c.UpdateStorageDef(context.Background(), "nas", v); err != nil {
			t.Fatalf("UpdateStorageDef: %v", err)
		}
		if s.method != http.MethodPut || s.path != "/api2/json/storage/nas" {
			t.Errorf("%s %s", s.method, s.path)
		}
		if s.body.Get("digest") != "d1" {
			t.Errorf("payload = %v", s.body)
		}
	})

	t.Run("delete", func(t *testing.T) {
		c, s := newSpy(t, `{"data":null}`)
		if err := c.DeleteStorageDef(context.Background(), "nas"); err != nil {
			t.Fatalf("DeleteStorageDef: %v", err)
		}
		if s.method != http.MethodDelete || s.path != "/api2/json/storage/nas" {
			t.Errorf("%s %s", s.method, s.path)
		}
		// Un DELETE ne porte pas de corps : PVE répond 501 avant même la couche
		// de schéma (PVX-031).
		if len(s.body) != 0 {
			t.Errorf("corps sur un DELETE = %v", s.body)
		}
	})
}

// Un identifiant inconnu répond 500, pas 404 — vérifié contre le nœud. Le
// pre-read qui déduit « une erreur = le nom est libre » reste juste ; ce test
// fige le fait que l'erreur remonte bien plutôt que de rendre une définition
// vide, qui ferait croire le stockage existant.
func TestStorageDefByIDSurfacesTheNodes500OnAnUnknownName(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"data":null,"message":"storage 'absent' does not exist\n"}`))
	})

	if _, err := c.StorageDefByID(context.Background(), "absent"); err == nil {
		t.Fatal("un identifiant inconnu doit remonter une erreur")
	}
}

// La capture réelle de GET /storage/{storage} REPÈTE bien « storage », mais le
// détail d'un objet demandé par son nom ne le répète pas toujours sur cette API
// — /access/users/{userid} et /cluster/backup/{id} ne le font pas. Le repli
// existe pour ça, et sans lui le post-read afficherait une ligne « stockage »
// vide sur une écriture qui a pourtant réussi.
func TestStorageDefByIDFillsBackAnAbsentIdentifier(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"nfs","content":"backup"}}`))
	})

	def, err := c.StorageDefByID(context.Background(), "nas-backup")
	if err != nil {
		t.Fatalf("StorageDefByID: %v", err)
	}
	if def.Storage != "nas-backup" {
		t.Errorf("storage = %q — l'identifiant demandé doit être remis", def.Storage)
	}
}

// Une liste illisible doit remonter en ERREUR, jamais en liste vide : « aucun
// stockage déclaré » et « je n'ai pas pu lire » mènent à des décisions opposées,
// et la première est la version rassurante de la seconde.
func TestStorageDefsSurfaceAFailureRatherThanAnEmptyList(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"data":null,"message":"Permission check failed\n"}`))
	})

	defs, err := c.StorageDefs(context.Background())
	if err == nil {
		t.Fatal("un 403 doit remonter une erreur")
	}
	if defs != nil {
		t.Errorf("aucune liste ne doit être rendue avec l'erreur, got %v", defs)
	}
}

func TestStorageDefTargetNamesWhereTheBytesLand(t *testing.T) {
	for name, tc := range map[string]struct {
		def  StorageDef
		want string
	}{
		"nfs":       {StorageDef{Server: "nas", Export: "/export"}, "nas:/export"},
		"cifs":      {StorageDef{Server: "nas", Share: "sauv"}, "nas:sauv"},
		"pbs":       {StorageDef{Server: "pbs", Datastore: "arch"}, "pbs:arch"},
		"serveur":   {StorageDef{Server: "nas"}, "nas"},
		"dir":       {StorageDef{Path: "/srv"}, "/srv"},
		"sans rien": {StorageDef{}, ""},
	} {
		if got := tc.def.Target(); got != tc.want {
			t.Errorf("%s : cible = %q, want %q", name, got, tc.want)
		}
	}

	// Un stockage désactivé n'est pas une destination de sauvegarde, même s'il
	// déclare « backup » : rien n'y sera écrit.
	off := StorageDef{Type: "pbs", Content: "backup", Disable: 1}
	if off.IsOffNodeBackupTarget() {
		t.Error("un stockage désactivé n'est pas une destination")
	}
	on := StorageDef{Type: "pbs", Content: "backup"}
	if !on.IsOffNodeBackupTarget() {
		t.Error("un PBS actif acceptant « backup » est une destination hors-nœud")
	}
	if len(on.ContentTypes()) != 1 {
		t.Errorf("ContentTypes = %v", on.ContentTypes())
	}
}
