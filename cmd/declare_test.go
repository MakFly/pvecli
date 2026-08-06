package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/iac"
	"github.com/dev-toolings/pvecli/internal/testutil"
)

func readDeclaration(t *testing.T, tfDir string) *iac.Declaration {
	t.Helper()
	d, err := iac.LoadDeclaration(tfDir)
	if err != nil {
		t.Fatalf("relecture de la déclaration : %v", err)
	}
	return d
}

func TestDeclareWritesTheVMAndItsServiceTags(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192",
		"--ip", "192.168.1.220/24", "--gateway", "192.168.1.1",
		"--with", "docker,postgresql", "--yes"); err != nil {
		t.Fatalf("vm declare : %v", err)
	}

	vm, ok := readDeclaration(t, tfDir).VMs["app-01"]
	if !ok {
		t.Fatal("app-01 absente de la déclaration")
	}
	if vm.VMID != 220 || vm.Cores != 2 || vm.Memory != 8192 {
		t.Errorf("déclaration = %+v", vm)
	}
	// The tag is the join with Ansible: without it the playbook matches no
	// host and exits 0, which looks exactly like a success.
	want := "managed,pvecli,svc_docker,svc_postgresql"
	if got := strings.Join(vm.Tags, ","); got != want {
		t.Errorf("tags = %q, attendu %q", got, want)
	}
}

// The one that makes `--memory 16384` a resize instead of a reset.
func TestDeclareOnlyTouchesTheFlagsThatWereGiven(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192",
		"--ip", "192.168.1.220/24", "--gateway", "192.168.1.1",
		"--with", "docker", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "vm", "declare", "app-01", "--memory", "16384", "--yes"); err != nil {
		t.Fatalf("redimensionnement : %v", err)
	}

	vm := readDeclaration(t, tfDir).VMs["app-01"]
	if vm.Memory != 16384 {
		t.Errorf("memory = %d, attendu 16384", vm.Memory)
	}
	for _, keep := range []struct{ field, got, want string }{
		{"ip", vm.IP, "192.168.1.220/24"},
		{"gateway", vm.Gateway, "192.168.1.1"},
		{"services", strings.Join(vm.Services, ","), "docker"},
	} {
		if keep.got != keep.want {
			t.Errorf("%s = %q, doit rester %q : ce qui n'est pas passé n'est pas touché", keep.field, keep.got, keep.want)
		}
	}
	if vm.Cores != 2 {
		t.Errorf("cores = %d, doit rester 2", vm.Cores)
	}
}

func TestDeclareDryRunWritesNothing(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192",
		"--with", "docker", "--dry-run"); err != nil {
		t.Fatalf("--dry-run : %v", err)
	}
	if _, err := os.Stat(filepath.Join(tfDir, iac.DeclarationFile)); err == nil {
		t.Error("--dry-run a écrit la déclaration")
	}
}

func TestDeclareRemovesAnEntry(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "vm", "declare", "app-01", "--remove", "--yes"); err != nil {
		t.Fatalf("--remove : %v", err)
	}
	if _, ok := readDeclaration(t, tfDir).VMs["app-01"]; ok {
		t.Error("app-01 est toujours déclarée après --remove")
	}
}

// A declaration is data Terraform reads; several VMs must coexist in it.
func TestDeclareKeepsTheOtherVMs(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	// Deux vmid distincts : un vmid Proxmox est unique, et le vmid n'est ici
	// qu'une donnée incidente — ce que ce test vérifie, c'est que retirer
	// app-01 n'emporte pas app-02.
	for _, guest := range []struct{ name, vmid string }{
		{"app-01", "220"},
		{"app-02", "221"},
	} {
		if _, _, err := run(t, "vm", "declare", guest.name,
			"--vmid", guest.vmid, "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := run(t, "vm", "declare", "app-01", "--remove", "--yes"); err != nil {
		t.Fatal(err)
	}
	d := readDeclaration(t, tfDir)
	if _, ok := d.VMs["app-02"]; !ok {
		t.Error("retirer app-01 a emporté app-02")
	}
}

// Proxmox n'a qu'un seul espace de vmid : un conteneur 221 interdit une VM 221.
// Sans ce refus, la collision ne sortirait qu'au « terraform apply », contre
// l'API, sous une forme illisible.
func TestDeclareRefusesAVMIDHeldByAnLXC(t *testing.T) {
	scaffoldDirs(t)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "221", "--cores", "2", "--memory", "8192", "--with", "", "--yes")
	if err == nil {
		t.Fatal("un vmid déjà tenu par un LXC doit être refusé")
	}
	// « le lxc » et pas « lxc » seul : le message contient déjà « vmid », donc
	// une sous-chaîne trop courte passerait sans rien prouver.
	for _, want := range []string{"221", "ct-01", "le lxc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("le message doit nommer %q : %v", want, err)
		}
	}
}

// Le déplacement d'un vmid est aussi une collision : le chemin de mise à jour
// doit être gardé comme celui de création.
func TestDeclareRefusesMovingOntoATakenVMID(t *testing.T) {
	scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "vm", "declare", "app-01", "--vmid", "221", "--yes")
	if err == nil {
		t.Fatal("déplacer app-01 sur le vmid du conteneur doit être refusé")
	}
	if !strings.Contains(err.Error(), "ct-01") {
		t.Errorf("le message doit nommer le propriétaire : %v", err)
	}
}

// Le faux positif à éviter : une entrée qui se redéclare avec SON vmid ne se
// heurte pas à elle-même, sinon tout redimensionnement serait refusé.
func TestDeclareAllowsRedeclaringItsOwnVMID(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "vm", "declare", "app-01", "--memory", "16384", "--yes"); err != nil {
		t.Fatalf("redimensionnement refusé à tort : %v", err)
	}
	if _, _, err := run(t, "vm", "declare", "app-01", "--vmid", "220", "--yes"); err != nil {
		t.Fatalf("redéclarer le même vmid sur la même entrée doit passer : %v", err)
	}
	if vm := readDeclaration(t, tfDir).VMs["app-01"]; vm.Memory != 16384 || vm.VMID != 220 {
		t.Errorf("déclaration = %+v", vm)
	}
}

// Un retrait ne revendique aucun vmid : le contrôle ne doit pas s'y appliquer,
// alors même que le vmid de l'entrée est présent dans la déclaration.
func TestDeclareRemoveIsNotBlockedByTheVMIDCheck(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "vm", "declare", "app-01", "--remove", "--yes"); err != nil {
		t.Fatalf("--remove refusé à tort : %v", err)
	}
	if _, ok := readDeclaration(t, tfDir).VMs["app-01"]; ok {
		t.Error("app-01 est toujours déclarée après --remove")
	}
}

// La ligne de base sans faux positif : deux invités à des vmid différents se
// déclarent tous les deux, quel que soit leur type.
func TestDeclareAcceptsDistinctVMIDs(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "vm", "declare", "app-02",
		"--vmid", "222", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatalf("un vmid libre doit passer : %v", err)
	}
	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "221", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatalf("un vmid libre doit passer : %v", err)
	}

	d := readDeclaration(t, tfDir)
	if len(d.VMs) != 2 || len(d.LXCs) != 1 {
		t.Errorf("déclaration = %d vms, %d lxcs", len(d.VMs), len(d.LXCs))
	}
}

func TestDeclareRefusesAnUnknownService(t *testing.T) {
	scaffoldDirs(t)

	_, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "postgres", "--yes")
	if err == nil {
		t.Fatal("un service inconnu doit arrêter la commande")
	}
	if !strings.Contains(err.Error(), "postgresql") {
		t.Errorf("le message doit proposer les ids valides : %v", err)
	}
}

// A pipeline must never block on a keystroke, and must never silently produce a
// bare VM either.
func TestDeclareWithoutServicesRefusesOutsideATerminal(t *testing.T) {
	scaffoldDirs(t)

	_, _, err := run(t, "vm", "declare", "app-01", "--vmid", "220", "--cores", "2", "--memory", "8192", "--yes")
	if err == nil {
		t.Fatal("sans --with et sans terminal, la commande doit refuser")
	}
	if !strings.Contains(err.Error(), "--with") {
		t.Errorf("le message doit nommer l'option qui répond à sa place : %v", err)
	}
}

func TestDeclareRequiresTheCreationFieldsOnlyOnCreation(t *testing.T) {
	scaffoldDirs(t)

	_, _, err := run(t, "vm", "declare", "app-01", "--cores", "2", "--with", "", "--yes")
	if err == nil {
		t.Fatal("une création sans --vmid ni --memory doit être refusée")
	}
	for _, want := range []string{"--vmid", "--memory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("le message doit nommer %s : %v", want, err)
		}
	}
}

// L'invariant central de la mission : sans --suggest-id, « vm declare » ne
// construit jamais de client, même quand un serveur de rejeu tourne et est
// prêt à répondre. len(srv.Requests) == 0 est la preuve -- pas une inférence
// depuis le message d'erreur, qui pourrait mentir.
func TestDeclareStaysOfflineWithoutSuggestID(t *testing.T) {
	scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "vm", "declare", "app-01", "--cores", "2", "--memory", "8192", "--with", "", "--yes")
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

// Le chemin nominal : --suggest-id contacte le nœud, et le vmid proposé se
// retrouve dans la déclaration écrite sur disque.
func TestDeclareSuggestIDWritesTheProposedVMID(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--suggest-id", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatalf("vm declare --suggest-id : %v", err)
	}

	vm, ok := readDeclaration(t, tfDir).VMs["app-01"]
	if !ok {
		t.Fatal("app-01 absente de la déclaration")
	}
	if vm.VMID != 235 {
		t.Errorf("vmid = %d, want 235 (proposé par la fixture cluster-nextid.json)", vm.VMID)
	}
}

// --vmid et --suggest-id sont deux sources pour le même champ : un refus, pas
// un mélange silencieux -- et le refus doit précéder tout appel réseau.
func TestDeclareRefusesVMIDWithSuggestID(t *testing.T) {
	scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--suggest-id", "--cores", "2", "--memory", "8192", "--with", "", "--yes")
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

// --remove et --suggest-id ensemble : --remove ne crée rien à numéroter, donc
// rien à suggérer non plus. Refusé avant tout appel réseau, entrée absente ou
// pas -- ce refus doit tirer AVANT le contrôle « exists-t-elle vraiment ? ».
func TestDeclareRefusesRemoveWithSuggestID(t *testing.T) {
	scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "vm", "declare", "app-01", "--remove", "--suggest-id", "--yes")
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

// --suggest-id sur une entrée déjà déclarée ne peut vouloir dire que
// « renumérote-la », ce que cette option ne fait pas -- côté Terraform, changer
// le vmid d'un guest vivant est un destroy+create, pas une mise à jour.
func TestDeclareRefusesSuggestIDOnAnExistingEntry(t *testing.T) {
	scaffoldDirs(t)
	if _, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatal(err)
	}

	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "vm", "declare", "app-01", "--suggest-id", "--yes")
	if err == nil {
		t.Fatal("--suggest-id sur une entrée déjà déclarée doit être refusé")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Errorf("code de sortie = %v, want 2", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("le refus doit précéder tout appel réseau, reçu : %v", srv.Requests)
	}
}

// /cluster/nextid ne connaît que le cluster, jamais la déclaration locale que
// cette même commande est en train d'écrire à côté : deux --suggest-id
// d'affilée avant un « iac apply » doivent recevoir deux vmid DIFFÉRENTS, pas
// silencieusement le même. Un seul appel réseau par invocation : l'ajustement
// est purement local.
func TestDeclareSuggestIDAvoidsALocalCollision(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	if _, _, err := run(t, "vm", "declare", "app-01",
		"--suggest-id", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatalf("app-01 : %v", err)
	}
	_, stderr, err := run(t, "vm", "declare", "app-02",
		"--suggest-id", "--cores", "2", "--memory", "8192", "--with", "", "--yes")
	if err != nil {
		t.Fatalf("app-02 : %v", err)
	}
	// Une troisième déclaration : si l'ajustement local ne faisait qu'un seul
	// pas (`if` au lieu d'un `for`), app-03 recevrait encore 236 -- déjà pris
	// par app-02 -- au lieu d'avancer jusqu'au prochain id vraiment libre.
	if _, _, err := run(t, "vm", "declare", "app-03",
		"--suggest-id", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatalf("app-03 : %v", err)
	}

	d := readDeclaration(t, tfDir)
	if d.VMs["app-01"].VMID != 235 {
		t.Errorf("app-01 vmid = %d, want 235", d.VMs["app-01"].VMID)
	}
	if d.VMs["app-02"].VMID != 236 {
		t.Errorf("app-02 vmid = %d, want 236 (235 déjà revendiqué par app-01)", d.VMs["app-02"].VMID)
	}
	if d.VMs["app-03"].VMID != 237 {
		t.Errorf("app-03 vmid = %d, want 237 (235 et 236 déjà revendiqués) : l'ajustement doit avancer d'autant de pas que nécessaire, pas d'un seul", d.VMs["app-03"].VMID)
	}
	if !strings.Contains(stderr, "déjà revendiqué") {
		t.Errorf("le message doit dire que l'ajustement a eu lieu :\n%s", stderr)
	}
	if len(srv.Requests) != 3 {
		t.Errorf("un seul appel réseau par invocation attendu (3 au total), reçu : %v", srv.Requests)
	}
}

// Le même compteur de vmid est partagé entre VM et LXC côté PVE : une
// suggestion sur une VM doit éviter un vmid déjà pris par un conteneur
// déclaré, symétrique de TestLXCDeclareSuggestIDAvoidsAVMIDHeldByAVM.
func TestDeclareSuggestIDAvoidsAVMIDHeldByALXC(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/cluster/nextid": "cluster-nextid.json",
	})
	point(t, srv.URL)

	if _, _, err := run(t, "lxc", "declare", "ct-01",
		"--vmid", "235", "--cores", "1", "--memory", "2048", "--template", "200",
		"--with", "", "--yes"); err != nil {
		t.Fatalf("ct-01 : %v", err)
	}
	if _, _, err := run(t, "vm", "declare", "app-01",
		"--suggest-id", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
		t.Fatalf("app-01 : %v", err)
	}

	d := readDeclaration(t, tfDir)
	if d.VMs["app-01"].VMID != 236 {
		t.Errorf("app-01 vmid = %d, want 236 (235 déjà tenu par le LXC ct-01)", d.VMs["app-01"].VMID)
	}
}

// stdout stays data: the declaration must survive `-o json | jq`.
func TestDeclareJSONOutputIsParsable(t *testing.T) {
	scaffoldDirs(t)

	stdout, _, err := run(t, "vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "docker", "--yes", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var vm iac.VM
	if err := json.Unmarshal([]byte(stdout), &vm); err != nil {
		t.Fatalf("stdout n'est pas du JSON : %v\n%s", err, stdout)
	}
	if vm.VMID != 220 {
		t.Errorf("vmid = %d dans le JSON de sortie", vm.VMID)
	}
}

// Declaring the same thing twice must be a no-op, not a rewrite.
func TestDeclareIsIdempotent(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	args := []string{"vm", "declare", "app-01",
		"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "docker", "--yes"}

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
