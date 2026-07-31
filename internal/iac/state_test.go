package iac

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubTerraform puts a fake `terraform` first in PATH.
//
// The point is not to avoid installing Terraform — it is installed on the
// machine this was written on. It is to make the argv observable: the criterion
// of PVX-043 is that pvectl NEVER writes the state, and the only way to prove a
// negative here is to look at every command actually issued.
func stubTerraform(t *testing.T, stdout string, exitCode int) (argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("le stub est un script shell")
	}

	dir := t.TempDir()
	argvFile = filepath.Join(dir, "argv")

	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + argvFile + "\n" +
		"cat <<'TFEOF'\n" + stdout + "\nTFEOF\n" +
		"exit " + itoa(exitCode) + "\n"

	if err := os.WriteFile(filepath.Join(dir, "terraform"), []byte(script), 0o755); err != nil { //nolint:gosec // a test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvFile
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

func argv(t *testing.T, file string) []string {
	t.Helper()
	body, err := os.ReadFile(file) //nolint:gosec // path built by the test
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

// A real `terraform show -json` payload for the lab's main.tf: one VM cloned
// from a template, tagged lab/managed/terraform.
const showJSON = `{
  "format_version": "1.0",
  "terraform_version": "1.15.8",
  "values": {
    "root_module": {
      "resources": [
        {
          "address": "proxmox_virtual_environment_vm.lab_app",
          "mode": "managed",
          "type": "proxmox_virtual_environment_vm",
          "name": "lab_app",
          "provider_name": "registry.terraform.io/bpg/proxmox",
          "values": {
            "vm_id": 210,
            "name": "lab-app-01",
            "node_name": "pve",
            "on_boot": true,
            "tags": ["terraform", "lab", "managed"],
            "cpu": [{"cores": 2, "sockets": 1, "type": "x86-64-v2-AES"}],
            "memory": [{"dedicated": 2048, "floating": 0}],
            "disk": [{"datastore_id": "local-lvm", "interface": "scsi0", "size": 20}],
            "network_device": [{"bridge": "vmbr0", "model": "virtio", "vlan_id": 0}],
            "initialization": [{"user_account": [{"username": "ops", "password": "hunter2"}]}]
          },
          "sensitive_values": {
            "initialization": [{"user_account": [{"password": true, "keys": true}]}]
          }
        },
        {
          "address": "data.proxmox_virtual_environment_vms.all",
          "mode": "data",
          "type": "proxmox_virtual_environment_vms",
          "values": {"vm_id": 9000}
        }
      ],
      "child_modules": [
        {
          "resources": [
            {
              "address": "module.web.proxmox_virtual_environment_container.ct",
              "mode": "managed",
              "type": "proxmox_virtual_environment_container",
              "values": {
                "vm_id": 120,
                "node_name": "pve",
                "tags": ["lab"],
                "cpu": [{"cores": 1}],
                "memory": [{"dedicated": 512}]
              }
            }
          ]
        }
      ]
    }
  }
}`

// The criterion of PVX-043, and the only one that cannot be checked by reading
// the output: `terraform show -json` is the ONE command issued. `refresh`,
// `apply` and `state rm` all write, and reaching for one of them in a later
// refactor would be an easy and completely invisible mistake.
func TestStateReadingNeverWritesTheState(t *testing.T) {
	log := stubTerraform(t, showJSON, 0)
	dir := t.TempDir()

	// A state file that must come back byte for byte.
	statePath := filepath.Join(dir, "terraform.tfstate")
	before := []byte(`{"version":4,"serial":7,"resources":[]}`)
	if err := os.WriteFile(statePath, before, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadState(context.Background(), dir, io.Discard); err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	if got := argv(t, log); len(got) != 1 || got[0] != "show -json" {
		t.Errorf("terraform doit être appelé une seule fois, en « show -json » ; reçu : %q", got)
	}

	after, err := os.ReadFile(statePath) //nolint:gosec // path built by the test
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("le state a été modifié :\navant %s\naprès %s", before, after)
	}
}

func TestStateExtractsWhatDriftWillCompare(t *testing.T) {
	stubTerraform(t, showJSON, 0)

	declared, err := ReadState(context.Background(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) != 2 {
		t.Fatalf("2 ressources attendues (la VM et le conteneur du module), reçu %d", len(declared))
	}

	// Sorted by vmid: the container (120) then the VM (210).
	ct, vm := declared[0], declared[1]

	if !ct.IsContainer() || ct.VMID != 120 {
		t.Errorf("la ressource du module enfant doit être remontée : %+v", ct)
	}
	if vm.VMID != 210 || vm.Name != "lab-app-01" || vm.Node != "pve" {
		t.Errorf("identité mal lue : %+v", vm)
	}
	// cpu and memory are nested BLOCKS, rendered as single-element lists.
	if vm.Cores != 2 || vm.Memory != 2048 {
		t.Errorf("cpu.cores / memory.dedicated mal lus : cores=%d memory=%d", vm.Cores, vm.Memory)
	}
	if vm.OnBoot == nil || !*vm.OnBoot {
		t.Errorf("on_boot mal lu : %v", vm.OnBoot)
	}
	if strings.Join(vm.Tags, ",") != "lab,managed,terraform" {
		t.Errorf("les tags doivent être triés pour être comparables : %v", vm.Tags)
	}
	if len(vm.Disks) != 1 || vm.Disks[0].Interface != "scsi0" || vm.Disks[0].Size != 20 {
		t.Errorf("disque mal lu : %+v", vm.Disks)
	}
	if len(vm.Networks) != 1 || vm.Networks[0].Bridge != "vmbr0" {
		t.Errorf("interface mal lue : %+v", vm.Networks)
	}
}

// The fixture above is hand-built to carry a child module and a data source,
// which the lab's single-resource main.tf does not have. This one is the real
// `terraform show -json` captured after the lab's own apply — the extraction
// has to work against what Terraform actually prints, not against what this
// package believed it prints.
//
// It is where the shape was confirmed: the bpg provider renders cpu, memory,
// disk and network_device as LISTS of objects, because they are nested blocks.
// Reading values["cpu"]["cores"] returns nothing at all, silently.
func TestExtractionMatchesARealTerraformShow(t *testing.T) {
	body, err := os.ReadFile("testdata/terraform-show.json")
	if err != nil {
		t.Fatal(err)
	}
	stubTerraform(t, string(body), 0)

	declared, err := ReadState(context.Background(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) != 1 {
		t.Fatalf("le state du lab déclare une ressource, reçu %d", len(declared))
	}

	d := declared[0]
	if d.VMID != 210 || d.Name != "lab-app-01" || d.Node != "pve" {
		t.Errorf("identité : %+v", d)
	}
	if d.Cores != 2 || d.Memory != 2048 {
		t.Errorf("les blocs cpu/memory sont des LISTES d'objets : cores=%d memory=%d", d.Cores, d.Memory)
	}
	if d.OnBoot == nil || !*d.OnBoot {
		t.Errorf("on_boot : %v", d.OnBoot)
	}
	if strings.Join(d.Tags, ",") != "lab,managed,terraform" {
		t.Errorf("tags : %v", d.Tags)
	}
	if len(d.Disks) != 1 || d.Disks[0].Size != 20 || d.Disks[0].Datastore != "local-lvm" {
		t.Errorf("disque : %+v", d.Disks)
	}
	if len(d.Networks) != 1 || d.Networks[0].Bridge != "vmbr0" {
		t.Errorf("réseau : %+v", d.Networks)
	}
}

// A data source describes what Terraform READS, not what it owns. Treating one
// as a declared resource would make drift report an orphan for something
// nothing ever created.
func TestDataSourcesAreNotDeclaredResources(t *testing.T) {
	stubTerraform(t, showJSON, 0)

	declared, err := ReadState(context.Background(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range declared {
		if strings.HasPrefix(d.Address, "data.") {
			t.Errorf("une data source est remontée comme ressource gérée : %s", d.Address)
		}
	}
}

// The fixture carries a cleartext password under a `sensitive_values` marker.
// It must not reach the output — and it does not, because every attribute is
// read by name and that name is not among them.
func TestNoSensitiveValueSurvivesTheExtraction(t *testing.T) {
	stubTerraform(t, showJSON, 0)

	declared, err := ReadState(context.Background(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range declared {
		if strings.Contains(dump(d), "hunter2") {
			t.Fatalf("une valeur sensible a fui : %+v", d)
		}
	}
}

func dump(d Declared) string {
	return d.Address + d.Name + d.Node + d.Type + strings.Join(d.Tags, ",")
}

// "Not applied yet" and "could not read" both show up as zero resources, and
// they call for opposite reactions. Terraform prints exactly this for a state
// that has never been applied.
func TestEmptyStateIsNotAReadFailure(t *testing.T) {
	stubTerraform(t, `{"format_version":"1.0"}`, 0)

	_, err := ReadState(context.Background(), t.TempDir(), io.Discard)

	var empty *EmptyStateError
	if !errors.As(err, &empty) {
		t.Fatalf("un state vide doit être une erreur distincte : %v", err)
	}
	if !strings.Contains(err.Error(), "pvectl iac apply") {
		t.Errorf("le message doit dire quoi faire ensuite : %v", err)
	}
}

// A directory the operator never configured is a `config set` away from being
// fixed. Saying "terraform show a échoué" instead would send them debugging
// Terraform.
func TestUnconfiguredDirectoryIsNamedAsSuch(t *testing.T) {
	_, err := ReadState(context.Background(), "", io.Discard)

	var missing *MissingDirError
	if !errors.As(err, &missing) {
		t.Fatalf("attendu MissingDirError, reçu %v", err)
	}
	if !strings.Contains(err.Error(), "pvectl config set iac.terraform_dir") {
		t.Errorf("le message doit donner la commande qui corrige : %v", err)
	}
}

// "terraform: command not found" is a dead end; the answer differs per
// platform, so the CLI carries it.
func TestMissingBinaryExplainsHowToInstallIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := ReadState(context.Background(), t.TempDir(), io.Discard)

	var missing *MissingToolError
	if !errors.As(err, &missing) {
		t.Fatalf("attendu MissingToolError, reçu %v", err)
	}
	if !strings.Contains(err.Error(), "brew install") {
		t.Errorf("le message doit dire comment l'installer : %v", err)
	}
}

// An uninitialised directory is the commonest failure, and terraform's own
// wording ("Missing required provider") does not say `terraform init`.
func TestUninitialisedDirectoryPointsAtTerraformInit(t *testing.T) {
	stubTerraform(t, "Error: Missing required provider", 1)

	_, err := ReadState(context.Background(), t.TempDir(), io.Discard)
	if err == nil {
		t.Fatal("un terraform en échec doit remonter une erreur")
	}
	if !strings.Contains(err.Error(), "terraform init") {
		t.Errorf("le message doit mentionner l'initialisation : %v", err)
	}
}
