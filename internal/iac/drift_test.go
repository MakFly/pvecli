package iac

import (
	"testing"

	"github.com/dev-toolings/pvecli/internal/pve"
)

func boolp(b bool) *bool { return &b }

func declaredVM() Declared {
	return Declared{
		Address: "proxmox_virtual_environment_vm.lab_app",
		Type:    TypeVM,
		VMID:    210, Name: "lab-app-01", Node: "pve",
		Cores: 2, Memory: 2048,
		Tags: []string{"lab", "managed", "terraform"}, OnBoot: boolp(true),
		Disks:    []DeclaredDisk{{Interface: "scsi0", Datastore: "local-lvm", Size: 20}},
		Networks: []DeclaredNIC{{Bridge: "vmbr0", Model: "virtio"}},
	}
}

func liveVM() Live {
	return Live{
		VMID: 210, Name: "lab-app-01", Node: "pve",
		Cores: 2, Memory: 2048,
		Tags: []string{"lab", "managed", "terraform"}, OnBoot: boolp(true),
		Disks:    map[string]LiveDisk{"scsi0": {Datastore: "local-lvm", SizeGiB: 20}},
		Networks: []LiveNIC{{Bridge: "vmbr0", Model: "virtio"}},
	}
}

// The baseline the whole command depends on: when nothing has been touched,
// nothing is reported. A drift detector that cries wolf is one an operator
// learns to ignore, and it then fails to report the drift that matters.
func TestNoDriftWhenDeclaredAndLiveAgree(t *testing.T) {
	report := Compare([]Declared{declaredVM()}, []Live{liveVM()})

	if report.HasDrift() {
		t.Errorf("aucune divergence ne devait être signalée : %+v", report.Findings)
	}
}

// The proof of PVX-044: a change made outside Terraform is caught, and the
// report says which attribute and both values.
func TestAChangeMadeOutsideTerraformIsCaught(t *testing.T) {
	live := liveVM()
	live.Cores = 4

	report := Compare([]Declared{declaredVM()}, []Live{live})

	if len(report.Findings) != 1 {
		t.Fatalf("une seule ressource a dérivé : %+v", report.Findings)
	}
	f := report.Findings[0]
	if f.Kind != KindModified || f.VMID != 210 {
		t.Fatalf("catégorie ou cible inattendue : %+v", f)
	}
	if len(f.Differences) != 1 {
		t.Fatalf("seul « cores » diffère : %+v", f.Differences)
	}
	if d := f.Differences[0]; d.Field != "cores" || d.Declared != "2" || d.Live != "4" {
		t.Errorf("le rapport doit donner les deux valeurs : %+v", d)
	}
}

// The three categories are three different problems: fix the code, remove the
// resource from the state, or adopt it. Merging them would hide which one.
func TestTheThreeCategoriesAreDistinguished(t *testing.T) {
	declared := []Declared{
		declaredVM(),
		{Address: "proxmox_virtual_environment_vm.gone", Type: TypeVM, VMID: 220, Name: "détruite-à-la-main"},
	}
	live := []Live{
		liveVM(),
		{VMID: 211, Name: "créée-à-la-main"},
	}

	report := Compare(declared, live)

	kinds := map[int]string{}
	for _, f := range report.Findings {
		kinds[f.VMID] = f.Kind
	}
	if kinds[220] != KindOrphan {
		t.Errorf("une ressource du state absente du nœud est orpheline : %v", kinds)
	}
	if kinds[211] != KindUnmanaged {
		t.Errorf("un guest du nœud absent du state n'est géré par personne : %v", kinds)
	}
	if _, reported := kinds[210]; reported {
		t.Errorf("la ressource conforme ne doit pas apparaître : %v", kinds)
	}

	if got := report.Only(KindOrphan); len(got.Findings) != 1 || got.Findings[0].VMID != 220 {
		t.Errorf("--only doit filtrer : %+v", got.Findings)
	}
}

// PVE lowercases tags and stores them in its own order. Reporting a drift
// because the node sorted a list differently is a false positive an operator
// cannot act on — they would "fix" main.tf and the drift would come back.
func TestTagOrderAndCaseAreNotDrift(t *testing.T) {
	d := declaredVM()
	d.Tags = []string{"Terraform", "LAB", "managed"}
	live := liveVM()
	live.Tags = []string{"lab", "managed", "terraform"}

	if report := Compare([]Declared{d}, []Live{live}); report.HasDrift() {
		t.Errorf("l'ordre et la casse des tags ne sont pas une dérive : %+v", report.Findings)
	}
}

// An attribute Terraform does not declare is an attribute it does not own.
// Comparing it would report a drift on a value nobody chose.
func TestUndeclaredAttributesAreNotCompared(t *testing.T) {
	d := declaredVM()
	d.Name, d.OnBoot, d.Tags = "", nil, nil

	live := liveVM()
	live.Name, live.OnBoot, live.Tags = "tout-autre-chose", boolp(false), []string{"rien-à-voir"}

	if report := Compare([]Declared{d}, []Live{live}); report.HasDrift() {
		t.Errorf("seuls les attributs déclarés se comparent : %+v", report.Findings)
	}
}

// The cloud-init drive (ide2) exists on every VM the provider clones and
// appears in no `disk` block: it is declared through `initialization`.
// Reporting it as an extra disk would flag every managed VM, forever.
func TestTheCloudInitDriveIsNotAnExtraDisk(t *testing.T) {
	live := liveVM()
	live.Disks["ide2"] = LiveDisk{Datastore: "local-lvm"}

	if report := Compare([]Declared{declaredVM()}, []Live{live}); report.HasDrift() {
		t.Errorf("un disque non déclaré n'est pas une dérive : %+v", report.Findings)
	}
}

// LiveFromPVE is where the two vocabularies meet, and every line of it is a
// schema fact: tags separated by semicolons, onboot as 0/1, a disk as an option
// string whose size carries its unit.
func TestLiveIsReadFromPVEsOwnVocabulary(t *testing.T) {
	cfg := pve.GuestConfig{
		"name":    "lab-app-01",
		"cores":   float64(2),
		"memory":  "2048",
		"tags":    "lab;managed;terraform",
		"onboot":  float64(1),
		"scsi0":   "local-lvm:vm-210-disk-0,iothread=1,size=20G",
		"ide2":    "local-lvm:vm-210-cloudinit,media=cdrom",
		"net0":    "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,tag=42",
		"vmgenid": "00000000-0000-4000-8000-000000000000",
	}

	l := LiveFromPVE(pve.Resource{VMID: 210, Node: "pve"}, cfg)

	if l.Cores != 2 || l.Memory != 2048 {
		t.Errorf("cores/memory : %d %d", l.Cores, l.Memory)
	}
	if l.OnBoot == nil || !*l.OnBoot {
		t.Errorf("onboot = 1 se lit « oui » : %v", l.OnBoot)
	}
	if len(l.Tags) != 3 {
		t.Errorf("les tags sont séparés par des points-virgules : %v", l.Tags)
	}
	if d := l.Disks["scsi0"]; d.Datastore != "local-lvm" || d.SizeGiB != 20 {
		t.Errorf("« local-lvm:vm-210-disk-0,size=20G » mal lu : %+v", d)
	}
	if len(l.Networks) != 1 {
		t.Fatalf("une interface attendue : %+v", l.Networks)
	}
	// The model is the KEY of the leading pair, not a value under "model".
	if n := l.Networks[0]; n.Bridge != "vmbr0" || n.Model != "virtio" || n.VLAN != 42 {
		t.Errorf("net0 mal lu : %+v", n)
	}
}

// A container names itself with `hostname`, and its disk is `rootfs`, which
// carries no index — KeysWithPrefix would miss it and the only disk of every
// container would go uncompared.
func TestAContainerIsReadWithItsOwnKeys(t *testing.T) {
	cfg := pve.GuestConfig{
		"hostname": "web",
		"cores":    float64(1),
		"memory":   float64(512),
		"rootfs":   "local-lvm:vm-120-disk-0,size=8G",
	}

	l := LiveFromPVE(pve.Resource{VMID: 120, Node: "pve"}, cfg)

	if l.Name != "web" {
		t.Errorf("un conteneur se nomme par « hostname » : %q", l.Name)
	}
	if d := l.Disks["rootfs"]; d.SizeGiB != 8 {
		t.Errorf("« rootfs » doit être comparé comme les autres disques : %+v", l.Disks)
	}
}

// An attribute PVE never wrote and one it wrote to 0 are different statements.
// Terraform declaring on_boot = false against a missing key is not a drift.
func TestAnAbsentOnBootIsNotAFalseOnBoot(t *testing.T) {
	l := LiveFromPVE(pve.Resource{VMID: 210}, pve.GuestConfig{})
	if l.OnBoot != nil {
		t.Errorf("une clé absente n'est pas « non » : %v", *l.OnBoot)
	}
}
