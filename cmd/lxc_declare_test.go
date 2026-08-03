package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/iac"
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
