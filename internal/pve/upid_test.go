package pve

import (
	"strings"
	"testing"
)

// A UPID captured from the lab, and one with the nine-digit pstart the node's
// own decoder explicitly allows.
const (
	realUPID  = "UPID:pve:000006FD:000043CD:6A6CAD90:vncshell::root@pam:"
	longUPID  = "UPID:pve:0011A2B3:1000043CD:6A6CAD90:qmclone:9000:automation@pve:"
	otherNode = "UPID:pve2:0011A2B3:000043CD:6A6CAD90:qmstart:210:automation@pve:"
)

func TestUPIDRoundTrip(t *testing.T) {
	for _, raw := range []string{realUPID, longUPID, otherNode} {
		u, err := ParseUPID(raw)
		if err != nil {
			t.Fatalf("ParseUPID(%q): %v", raw, err)
		}
		if u.String() != raw {
			t.Errorf("aller-retour = %q, want %q", u.String(), raw)
		}
	}
}

func TestUPIDFields(t *testing.T) {
	u, err := ParseUPID(realUPID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Node != "pve" {
		t.Errorf("Node = %q", u.Node)
	}
	if u.Type != "vncshell" {
		t.Errorf("Type = %q", u.Type)
	}
	if u.ID != "" {
		t.Errorf("ID = %q, ce champ est vide pour un vncshell", u.ID)
	}
	if u.User != "root@pam" {
		t.Errorf("User = %q", u.User)
	}
	// Hex, not decimal: 0x6FD = 1789, which is the pid the lab reported.
	if u.PID != 1789 {
		t.Errorf("PID = %d, want 1789 (0x6FD)", u.PID)
	}
}

// The node to poll comes from the UPID, never from the default context. This
// is what lets a task triggered anywhere be followed from anywhere.
func TestUPIDCarriesItsNode(t *testing.T) {
	u, err := ParseUPID(otherNode)
	if err != nil {
		t.Fatal(err)
	}
	if u.Node != "pve2" {
		t.Errorf("Node = %q, want pve2 — présumer le nœud par défaut est le bug que ce champ évite", u.Node)
	}
}

func TestUPIDMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"UPID:pve:zzzz:000043CD:6A6CAD90:qmstart:210:root@pam:",
		"UPID:pve:000006FD:000043CD:6A6CAD90:qmstart:210:root@pam", // pas de deux-points final
		"OK",
	} {
		if _, err := ParseUPID(bad); err == nil {
			t.Errorf("ParseUPID(%q) doit échouer", bad)
		} else if !strings.Contains(err.Error(), "UPID") {
			t.Errorf("l'erreur doit être explicite: %v", err)
		}
	}
}

// A mutation may answer synchronously; that answer must not be polled.
func TestIsUPID(t *testing.T) {
	if !IsUPID(realUPID) {
		t.Error("un vrai UPID doit être reconnu")
	}
	for _, notUPID := range []string{"", "OK", "9.2.2"} {
		if IsUPID(notUPID) {
			t.Errorf("IsUPID(%q) = true", notUPID)
		}
	}
}
