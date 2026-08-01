package cmd

import (
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/pve"
)

// The Access warning is only useful where an Access application can actually
// sit. Raised on a LAN endpoint it would be noise, and a warning that cries
// wolf on every run is a warning nobody reads on the run that matters.
func TestIsPublicHost(t *testing.T) {
	cases := map[string]bool{
		"https://192.168.1.23:8006":    false,
		"https://10.0.0.5:8006":        false,
		"https://172.16.4.1:8006":      false,
		"https://127.0.0.1:8006":       false,
		"https://pve:8006":             false,
		"https://pve.lan:8006":         false,
		"https://pve.local:8006":       false,
		"https://pve.pulseview.app":    true,
		"https://pve.example.com:8006": true,
		"https://203.0.113.7:8006":     true,
	}
	for endpoint, want := range cases {
		if got := isPublicHost(endpoint); got != want {
			t.Errorf("isPublicHost(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

func TestAccessWarningOnlyFiresOnAPublicEndpointWithoutAServiceToken(t *testing.T) {
	const needle = "service token Cloudflare"

	got := warnings("automation@pve!pvectl", "https://pve.pulseview.app", pve.TrustSystem, false)
	if !strings.Contains(strings.Join(got, "\n"), needle) {
		t.Errorf("un endpoint public sans service token doit être signalé, got %v", got)
	}

	got = warnings("automation@pve!pvectl", "https://pve.pulseview.app", pve.TrustSystem, true)
	if strings.Contains(strings.Join(got, "\n"), needle) {
		t.Errorf("aucun avertissement quand le service token est présent, got %v", got)
	}

	got = warnings("automation@pve!pvectl", "https://192.168.1.23:8006", pve.TrustSystem, false)
	if strings.Contains(strings.Join(got, "\n"), needle) {
		t.Errorf("aucun avertissement sur un endpoint du LAN, got %v", got)
	}
}
