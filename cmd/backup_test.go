package cmd

import (
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/testutil"
)

// The listing shows what exists. --check shows what does NOT, which is the one
// thing a listing can never surface by listing.
func TestCheckReportsGuestsWithNoBackup(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/storage":               "storage.json",
		"GET /api2/json/nodes/pve/storage/local/content": "backups.json",
		"GET /api2/json/nodes/pve/qemu":                  "qemu.json",
		"GET /api2/json/nodes/pve/lxc":                   "qemu-empty.json",
	})
	point(t, srv.URL)

	stdout, _, err := run(t, "backup", "ls", "--check", "--node", "pve")
	if err != nil {
		t.Fatalf("backup ls --check: %v", err)
	}

	// The fixture holds one archive, for guest 212. Every other guest of the
	// inventory has an infinite RPO and must be named.
	if strings.Contains(stdout, "212") {
		t.Errorf("212 est sauvegardé, il ne doit pas figurer parmi les non protégés:\n%s", stdout)
	}
	if !strings.Contains(stdout, "INFINI") {
		t.Errorf("un guest sans sauvegarde a un RPO infini, et ça doit se lire:\n%s", stdout)
	}
}

// A restoration onto a live guest destroys the very thing the backup was meant
// to protect — before anyone has checked the archive is any good.
func TestRestoreRefusesAnOccupiedVMID(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/qemu/211/status/current": "qemu-status.json",
		"POST /api2/json/nodes/pve/qemu":                   "upid.json",
	})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "restore",
		"local:backup/vzdump-qemu-212-2026_07_31-20_36_33.vma.zst",
		"--newid", "211", "--node", "pve", "--yes")
	if err == nil {
		t.Fatal("restaurer sur un vmid occupé doit être refusé")
	}
	if !strings.Contains(err.Error(), "--overwrite") {
		t.Errorf("le refus doit nommer la porte de sortie : %v", err)
	}
	for _, req := range srv.Requests {
		if strings.HasPrefix(req, "POST ") {
			t.Fatalf("le refus doit précéder l'écriture : %v", srv.Requests)
		}
	}
}

// An archive names the family it holds. A volid that names none cannot be
// restored, and saying so beats letting the node fail on a half-built request.
func TestRestoreRefusesAVolidItCannotRead(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{})
	point(t, srv.URL)

	_, _, err := run(t, "backup", "restore", "local:iso/debian.iso", "--newid", "910")
	if err == nil || !strings.Contains(err.Error(), "vzdump-qemu") {
		t.Errorf("un volid qui n'est pas une sauvegarde doit être refusé avec un exemple : %v", err)
	}
	if len(srv.Requests) != 0 {
		t.Errorf("aucune requête ne doit partir : %v", srv.Requests)
	}
}

// `dr drill` is the only command whose execution destroys something that works.
// Its default is therefore the simulation, and --execute is deliberately not a
// shortcut around conducting the exercise by hand.
func TestDrillSimulatesByDefault(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/storage":               "storage.json",
		"GET /api2/json/nodes/pve/storage/local/content": "backups.json",
	})
	point(t, srv.URL)

	stdout, _, err := run(t, "dr", "drill", "--vmid", "212", "--node", "pve")
	if err != nil {
		t.Fatalf("dr drill: %v", err)
	}
	if !strings.Contains(stdout, "RPO actuel") {
		t.Errorf("le scénario doit afficher le RPO courant:\n%s", stdout)
	}
	for _, req := range srv.Requests {
		if !strings.HasPrefix(req, "GET ") {
			t.Fatalf("la simulation n'écrit rien : %v", srv.Requests)
		}
	}
}
