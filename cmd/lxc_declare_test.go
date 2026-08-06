package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/iac"
	"github.com/dev-toolings/pvecli/internal/testutil"
)

func TestLXCDeclareWritesTheContainerAndItsServiceTags(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--ip", "192.168.1.221/24", "--gateway", "192.168.1.1",
		"--with", "docker", "--yes"); err != nil {
		t.Fatalf("lxc declare : %v", err)
	}

	ct, ok := readDeclaration(t, tfDir).LXCs["ct-01"]
	if !ok {
		t.Fatal("ct-01 absent de la déclaration")
	}
	if ct.VMID != 221 || ct.Cores != 1 || ct.Memory != 2048 || ct.Template != 200 {
		t.Errorf("déclaration = %+v", ct)
	}
	if !ct.Unprivileged {
		t.Error("unprivileged doit valoir true par défaut à la création")
	}
	want := "managed,pvecli,svc_docker"
	if got := strings.Join(ct.Tags, ","); got != want {
		t.Errorf("tags = %q, attendu %q", got, want)
	}
}

// The LXC declaration lives in the same file as VMs, under its own key, and
// neither erases the other.
func TestLXCDeclareCoexistsWithVMs(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}

	d := readDeclaration(t, tfDir)
	if _, ok := d.VMs["app-01"]; !ok {
		t.Error("déclarer un LXC a effacé la VM")
	}
	if _, ok := d.LXCs["ct-01"]; !ok {
		t.Error("ct-01 absent")
	}
}

// La symétrie du contrôle de vmid n'est pas gratuite : elle se vérifie dans
// l'autre sens. Une VM 220 interdit un conteneur 220, l'espace de vmid Proxmox
// étant unique.
func TestLXCDeclareRefusesAVMIDHeldByAVM(t *testing.T) {
	scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "220", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes")
	if err == nil {
		t.Fatal("un vmid déjà tenu par une VM doit être refusé")
	}
	// « la vm » et pas « vm » seul : le message contient déjà « vmid », donc
	// une sous-chaîne trop courte passerait sans rien prouver.
	for _, want := range []string{"220", "app-01", "la vm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("le message doit nommer %q : %v", want, err)
		}
	}
}

// Même faux positif à éviter que côté VM : un conteneur qui se redéclare avec
// SON vmid ne se heurte pas à lui-même.
func TestLXCDeclareAllowsRedeclaringItsOwnVMID(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01", "--memory", "4096", "--yes"); err != nil {
		t.Fatalf("redimensionnement refusé à tort : %v", err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01", "--vmid", "221", "--yes"); err != nil {
		t.Fatalf("redéclarer le même vmid sur la même entrée doit passer : %v", err)
	}
	if ct := readDeclaration(t, tfDir).LXCs["ct-01"]; ct.Memory != 4096 || ct.VMID != 221 {
		t.Errorf("déclaration = %+v", ct)
	}
}

// --template is mandatory at creation: there is no shared default ctid to
// clone from, unlike a VM's var.template_vm_id.
func TestLXCDeclareRequiresTemplateOnCreation(t *testing.T) {
	scaffoldDirs(t)

	_, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--with", "", "--yes")
	if err == nil {
		t.Fatal("une création sans --template doit être refusée")
	}
	if !strings.Contains(err.Error(), "--template") {
		t.Errorf("le message doit nommer --template : %v", err)
	}
}

// Only the flags actually given move -- a resize must not reset the template.
func TestLXCDeclareOnlyTouchesTheFlagsThatWereGiven(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "docker", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01", "--memory", "4096", "--yes"); err != nil {
		t.Fatalf("redimensionnement : %v", err)
	}

	ct := readDeclaration(t, tfDir).LXCs["ct-01"]
	if ct.Memory != 4096 {
		t.Errorf("memory = %d, attendu 4096", ct.Memory)
	}
	if ct.Template != 200 {
		t.Errorf("template = %d, doit rester 200 : ce qui n'est pas passé n'est pas touché", ct.Template)
	}
}

func TestLXCDeclareRemovesAnEntry(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01", "--remove", "--yes"); err != nil {
		t.Fatalf("--remove : %v", err)
	}
	if _, ok := readDeclaration(t, tfDir).LXCs["ct-01"]; ok {
		t.Error("ct-01 est toujours déclaré après --remove")
	}
}

func TestLXCDeclareDryRunWritesNothing(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "docker", "--dry-run"); err != nil {
		t.Fatalf("--dry-run : %v", err)
	}
	if _, err := os.Stat(filepath.Join(tfDir, iac.DeclarationFile)); err == nil {
		t.Error("--dry-run a écrit la déclaration")
	}
}

// Symétrique de TestDeclareStaysOfflineWithoutSuggestID côté LXC : sans
// --suggest-id, aucune requête ne doit partir, même vers un serveur prêt à
// répondre.
func TestLXCDeclareStaysOfflineWithoutSuggestID(t *testing.T) {
	scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "lxc", "declare", "ct-01",
		"--cores", "1", "--memory", "2048", "--template", "200", "--with", "", "--yes")
	if err == nil {
		t.Fatal("une création sans --vmid ni --suggest-id doit être refusée")
	}
	if !strings.Contains(err.Error(), "obligatoire") {
		t.Errorf("le message doit rappeler le champ obligatoire : %v", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir sans --suggest-id, reçu : %v", srv.Requests)
	}
}

// Chemin nominal côté LXC : --suggest-id contacte le nœud, le vmid proposé se
// retrouve dans la déclaration écrite sur disque.
func TestLXCDeclareSuggestIDWritesTheProposedVMID(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--suggest-id", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatalf("lxc declare --suggest-id : %v", err)
	}

	ct, ok := readDeclaration(t, tfDir).LXCs["ct-01"]
	if !ok {
		t.Fatal("ct-01 absent de la déclaration")
	}
	if ct.VMID != 235 {
		t.Errorf("vmid = %d, want 235 (proposé par la fixture cluster-nextid.json)", ct.VMID)
	}
}

// --vmid et --suggest-id ensemble : refusé avant tout appel réseau, côté LXC
// aussi.
func TestLXCDeclareRefusesVMIDWithSuggestID(t *testing.T) {
	scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--suggest-id", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes")
	if err == nil {
		t.Fatal("--vmid et --suggest-id ensemble doivent être refusés")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Errorf("code de sortie = %v, want 2", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("le refus doit précéder tout appel réseau, reçu : %v", srv.Requests)
	}
}

// --remove et --suggest-id ensemble, côté LXC -- symétrique de
// TestDeclareRefusesRemoveWithSuggestID.
func TestLXCDeclareRefusesRemoveWithSuggestID(t *testing.T) {
	scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "lxc", "declare", "ct-01", "--remove", "--suggest-id", "--yes")
	if err == nil {
		t.Fatal("--remove et --suggest-id ensemble doivent être refusés")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Errorf("code de sortie = %v, want 2", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("le refus doit précéder tout appel réseau, reçu : %v", srv.Requests)
	}
}

// --suggest-id sur un conteneur déjà déclaré, côté LXC -- symétrique de
// TestDeclareRefusesSuggestIDOnAnExistingEntry.
func TestLXCDeclareRefusesSuggestIDOnAnExistingEntry(t *testing.T) {
	scaffoldDirs(t)
	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}

	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "lxc", "declare", "ct-01", "--suggest-id", "--yes")
	if err == nil {
		t.Fatal("--suggest-id sur un conteneur déjà déclaré doit être refusé")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Errorf("code de sortie = %v, want 2", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("le refus doit précéder tout appel réseau, reçu : %v", srv.Requests)
	}
}

// Symétrique de TestDeclareSuggestIDAvoidsALocalCollision côté LXC.
func TestLXCDeclareSuggestIDAvoidsALocalCollision(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--suggest-id", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatalf("ct-01 : %v", err)
	}
	_, stderr, err := run(t, "lxc", "declare", "ct-02",
		"--suggest-id", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes")
	if err != nil {
		t.Fatalf("ct-02 : %v", err)
	}
	// Une troisième déclaration : si l'ajustement local ne faisait qu'un seul
	// pas (`if` au lieu d'un `for`), ct-03 recevrait encore 236 -- déjà pris
	// par ct-02 -- au lieu d'avancer jusqu'au prochain id vraiment libre.
	if _, _, err := run(t, "lxc", "declare", "ct-03",
		"--suggest-id", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatalf("ct-03 : %v", err)
	}

	d := readDeclaration(t, tfDir)
	if d.LXCs["ct-01"].VMID != 235 {
		t.Errorf("ct-01 vmid = %d, want 235", d.LXCs["ct-01"].VMID)
	}
	if d.LXCs["ct-02"].VMID != 236 {
		t.Errorf("ct-02 vmid = %d, want 236 (235 déjà revendiqué par ct-01)", d.LXCs["ct-02"].VMID)
	}
	if d.LXCs["ct-03"].VMID != 237 {
		t.Errorf("ct-03 vmid = %d, want 237 (235 et 236 déjà revendiqués) : l'ajustement doit avancer d'autant de pas que nécessaire, pas d'un seul", d.LXCs["ct-03"].VMID)
	}
	if !strings.Contains(stderr, "déjà revendiqué") {
		t.Errorf("le message doit dire que l'ajustement a eu lieu :\n%s", stderr)
	}
	if len(srv.Requests) != 3 {
		t.Errorf("un seul appel réseau par invocation attendu (3 au total), reçu : %v", srv.Requests)
	}
}

// Le même compteur de vmid est partagé entre VM et LXC côté PVE : une
// suggestion sur un conteneur doit éviter un vmid déjà pris par une VM
// déclarée, pas seulement par un autre conteneur.
func TestLXCDeclareSuggestIDAvoidsAVMIDHeldByAVM(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "235", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatalf("app-01 : %v", err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--suggest-id", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatalf("ct-01 : %v", err)
	}

	d := readDeclaration(t, tfDir)
	if d.LXCs["ct-01"].VMID != 236 {
		t.Errorf("ct-01 vmid = %d, want 236 (235 déjà tenu par la VM app-01)", d.LXCs["ct-01"].VMID)
	}
}

// Declaring the same thing twice must be a no-op, not a rewrite.
func TestLXCDeclareIsIdempotent(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	args := []string{"lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200", "--with", "docker", "--yes"}

	if _, _, err := run(t, args...); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(tfDir, iac.DeclarationFile)) //nolint:gosec // path built by the test
	if err != nil {
		t.Fatal(err)
	}

	_, stderr, err := run(t, args...)
	if err != nil {
		t.Fatalf("second passage : %v", err)
	}
	if !strings.Contains(stderr, "aucun changement") {
		t.Errorf("le second passage doit annoncer qu'il n'y a rien à faire :\n%s", stderr)
	}
	second, err := os.ReadFile(filepath.Join(tfDir, iac.DeclarationFile)) //nolint:gosec // path built by the test
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("le fichier a changé alors que la déclaration est identique")
	}
}
