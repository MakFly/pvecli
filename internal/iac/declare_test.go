package iac

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first `vm declare` in a directory has nothing to read and must still work.
func TestLoadDeclarationTreatsAMissingFileAsEmpty(t *testing.T) {
	d, err := LoadDeclaration(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDeclaration: %v", err)
	}
	if len(d.VMs) != 0 {
		t.Errorf("VMs = %v, attendu vide", d.VMs)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := VM{VMID: 220, Cores: 2, Memory: 8192, Disk: 20, IP: "192.168.1.220/24",
		Services: []string{"docker"}, Tags: []string{"managed", "pvecli", "svc_docker"}}

	d := &Declaration{VMs: map[string]VM{"app-01": want}}
	if err := d.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := LoadDeclaration(dir)
	if err != nil {
		t.Fatalf("LoadDeclaration: %v", err)
	}
	got, ok := back.VMs["app-01"]
	if !ok {
		t.Fatal("app-01 absente après relecture")
	}
	if got.Memory != want.Memory || got.IP != want.IP || len(got.Tags) != len(want.Tags) {
		t.Errorf("relecture = %+v, écrit = %+v", got, want)
	}
}

// Unstable bytes would make every `iac plan` report a change that is not one.
func TestRenderIsStable(t *testing.T) {
	d := &Declaration{VMs: map[string]VM{
		"b": {VMID: 2, Cores: 1, Memory: 512, Tags: []string{"managed", "pvecli"}},
		"a": {VMID: 1, Cores: 1, Memory: 512, Tags: []string{"managed", "pvecli"}},
	}}
	first, err := d.Render()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := d.Render()
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("rendu instable au tour %d", i)
		}
	}
	if strings.Index(string(first), `"a"`) > strings.Index(string(first), `"b"`) {
		t.Error("les clés doivent sortir triées")
	}
}

// A half-written file would be read by the next plan as a missing VM, and
// therefore as a VM to destroy.
func TestSaveLeavesNoTemporaryFileBehind(t *testing.T) {
	dir := t.TempDir()
	d := &Declaration{VMs: map[string]VM{"a": {VMID: 100, Cores: 1, Memory: 512}}}
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != DeclarationFile {
			t.Errorf("résidu dans le dossier terraform : %s", e.Name())
		}
	}
}

func TestLoadDeclarationRefusesABrokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DeclarationFile), []byte("{ pas du json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDeclaration(dir); err == nil {
		t.Error("un fichier cassé doit arrêter la commande, pas être réécrit")
	}
}

func TestValidate(t *testing.T) {
	base := VM{VMID: 220, Cores: 2, Memory: 8192}

	cases := []struct {
		name string
		vm   VM
		id   string
		want string // substring expected in the error; "" means it must pass
	}{
		{"correcte", base, "app-01", ""},
		{"le piège des 8 Mio", VM{VMID: 220, Cores: 2, Memory: 8}, "app-01", "8192"},
		{"vmid réservé", VM{VMID: 42, Cores: 2, Memory: 8192}, "app-01", "vmid"},
		{"zéro cœur", VM{VMID: 220, Cores: 0, Memory: 8192}, "app-01", "cores"},
		{"nom en majuscules", base, "App01", "invalide"},
		{"nom avec un point", base, "app.01", "invalide"},
		{"ip sans préfixe", VM{VMID: 220, Cores: 2, Memory: 8192, IP: "192.168.1.220"}, "app-01", "préfixe"},
		{"passerelle en dhcp", VM{VMID: 220, Cores: 2, Memory: 8192, IP: "dhcp", Gateway: "192.168.1.1"}, "app-01", "passerelle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.vm.Validate(tc.id)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refusée à tort : %v", err)
			case tc.want != "" && err == nil:
				t.Errorf("acceptée à tort, attendu un message contenant %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("message = %q, attendu contenant %q", err, tc.want)
			}
		})
	}
}

func TestSetTagsSortsAndKeepsTheOwnership(t *testing.T) {
	vm := VM{}
	vm.SetTags([]string{"svc_postgresql", "svc_docker"})
	want := []string{"managed", "pvecli", "svc_docker", "svc_postgresql"}
	if strings.Join(vm.Tags, ",") != strings.Join(want, ",") {
		t.Errorf("Tags = %v, attendu %v", vm.Tags, want)
	}
}

func TestDiff(t *testing.T) {
	before := &VM{VMID: 220, Cores: 2, Memory: 8192, Disk: 20}
	after := &VM{VMID: 220, Cores: 2, Memory: 16384, Disk: 20}

	got := Diff(before, after)
	if len(got) != 1 {
		t.Fatalf("Diff = %+v, un seul champ change", got)
	}
	if got[0].Field != "memory" || got[0].Before != "8192 Mio" || got[0].After != "16384 Mio" {
		t.Errorf("Diff = %+v", got[0])
	}
}

// A zero-valued field must stay empty rather than render a bare unit -- " Mio"
// against "8192 Mio" would look like an addition out of nothing.
func TestDiffDoesNotInventUnitsForUnsetFields(t *testing.T) {
	for _, ch := range Diff(nil, &VM{VMID: 220, Cores: 1, Memory: 512}) {
		if strings.TrimSpace(ch.After) != ch.After || strings.HasPrefix(ch.After, " ") {
			t.Errorf("champ %s rendu comme %q", ch.Field, ch.After)
		}
		if ch.Field == "disk" {
			t.Errorf("disk non renseigné ne doit pas apparaître dans le diff : %q", ch.After)
		}
	}
}

func TestDiffOfAnAdditionAndARemoval(t *testing.T) {
	vm := &VM{VMID: 220, Cores: 2, Memory: 8192}

	added := Diff(nil, vm)
	if len(added) == 0 {
		t.Fatal("une création doit tout lister")
	}
	for _, ch := range added {
		if ch.Before != "" {
			t.Errorf("champ %s : avant = %q, attendu vide sur une création", ch.Field, ch.Before)
		}
	}

	removed := Diff(vm, nil)
	for _, ch := range removed {
		if ch.After != "" {
			t.Errorf("champ %s : après = %q, attendu vide sur un retrait", ch.Field, ch.After)
		}
	}
}
