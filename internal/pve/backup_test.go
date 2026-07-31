package pve

import (
	"context"
	"testing"
	"time"

	"github.com/MakFly/pvectl/internal/testutil"
)

// remove=1 is the API's default and it means "apply the retention policy",
// i.e. delete older archives. A backup command that silently prunes can destroy
// the only copy of something.
func TestBackupNeverPrunesUnlessAsked(t *testing.T) {
	v := VZDumpOptions{VMIDs: []int{212}, Storage: "local", Mode: ModeSnapshot, Compress: "zstd"}.Values()

	if v.Get("remove") != "0" {
		t.Errorf("remove doit valoir 0 par défaut: %v", v)
	}
	if v.Get("vmid") != "212" {
		t.Errorf("vmid = %q", v.Get("vmid"))
	}

	pruning := VZDumpOptions{VMIDs: []int{212}, Prune: true}.Values()
	if pruning.Get("remove") != "1" {
		t.Errorf("--prune doit envoyer remove=1: %v", pruning)
	}

	// --all and a vmid list are exclusive: sending both makes PVE ignore one of
	// them, and which one is not something to find out on a production node.
	all := VZDumpOptions{All: true, VMIDs: []int{212}}.Values()
	if all.Get("all") != "1" {
		t.Errorf("--all doit être envoyé: %v", all)
	}
	if _, present := all["vmid"]; present {
		t.Errorf("--all ne doit pas envoyer vmid: %v", all)
	}
}

// The archive already knows what it holds. Deducing the family from the volid
// removes a flag the operator could get wrong — restoring a container as a VM
// fails late and confusingly.
func TestArchiveTellsItsOwnKind(t *testing.T) {
	cases := map[string]GuestType{
		"local:backup/vzdump-qemu-212-2026_07_31-20_36_33.vma.zst":  TypeQEMU,
		"local:backup/vzdump-lxc-120-2026_07_31-20_36_33.tar.zst":   TypeLXC,
		"local:backup/vzdump-openvz-120-2026_07_31-20_36_33.tar.gz": TypeLXC,
	}
	for volid, want := range cases {
		got, ok := ArchiveGuestType(volid)
		if !ok || got != want {
			t.Errorf("ArchiveGuestType(%q) = %q, %v — want %q", volid, got, ok, want)
		}
	}
	if _, ok := ArchiveGuestType("local:iso/debian.iso"); ok {
		t.Error("un ISO n'est pas une sauvegarde")
	}

	// The vmid comes out of the same name: `backup ls --check` needs it to tell
	// which guests are protected, and not every storage type reports it.
	if id, ok := ArchiveVMID("local:backup/vzdump-qemu-212-2026_07_31-20_36_33.vma.zst"); !ok || id != 212 {
		t.Errorf("ArchiveVMID = %d, %v — want 212", id, ok)
	}
}

// force is what lets a restoration replace an existing guest. It must never be
// sent unless it was asked for: the whole point of restoring to a NEW vmid is
// to test an archive without destroying the thing it was meant to protect.
func TestRestoreNeverOverwritesUnlessAsked(t *testing.T) {
	v := RestoreOptions{Archive: "local:backup/vzdump-qemu-212-x.vma.zst"}.Values()

	if v.Get("archive") == "" {
		t.Errorf("archive doit être envoyée: %v", v)
	}
	if _, present := v["force"]; present {
		t.Errorf("force ne doit pas partir sans --overwrite: %v", v)
	}
	if (RestoreOptions{Overwrite: true}).Values().Get("force") != "1" {
		t.Error("--overwrite doit envoyer force=1")
	}
}

// The age of the most recent archive IS the RPO. Everything written since is in
// no archive at all.
func TestArchiveAgeIsTheRPO(t *testing.T) {
	now := time.Date(2026, 7, 31, 21, 0, 0, 0, time.UTC)
	a := Archive{Volume: Volume{CTime: now.Add(-90 * time.Minute).Unix()}}

	if got := a.Age(now); got != 90*time.Minute {
		t.Errorf("Age = %v, want 1h30", got)
	}
	if got := FormatAge(a.Age(now)); got != "1 h" {
		t.Errorf("FormatAge = %q", got)
	}
	// A guest with no archive has no age to show — and an infinite RPO.
	if got := FormatAge(0); got != "—" {
		t.Errorf("FormatAge(0) = %q", got)
	}
}

// Only storages declaring the backup content type may hold archives. Asking the
// others earns a 400 that blames the parameters.
func TestOnlyBackupStoragesAreQueried(t *testing.T) {
	srv := testutil.New(t, "../../testdata", map[string]string{
		"GET /api2/json/nodes/pve/storage": "storage.json",
	})
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "automation@pve!pvectl", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	eligible, err := c.BackupStorages(context.Background(), "pve")
	if err != nil {
		t.Fatalf("BackupStorages: %v", err)
	}
	for _, s := range eligible {
		if !s.Accepts("backup") {
			t.Errorf("%s n'accepte pas « backup » et ne devrait pas être listé", s.Storage)
		}
	}
}
