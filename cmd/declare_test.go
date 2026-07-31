package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/iac"
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

	for _, name := range []string{"app-01", "app-02"} {
		if _, _, err := run(t, "vm", "declare", name,
			"--vmid", "220", "--cores", "2", "--memory", "8192", "--with", "", "--yes"); err != nil {
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
