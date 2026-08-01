package cmd

import (
	"errors"
	"testing"

	"github.com/MakFly/pvecli/internal/pve"
)

// --no-tls-verify only means something against an origin that speaks TLS.
// Accepting it on an http:// service would write an option cloudflared ignores,
// and leave the operator believing the certificate question was settled.
func TestNoTLSVerifyIsRefusedOnAPlainHTTPOrigin(t *testing.T) {
	_, _, err := run(t, "cf", "route", "add", "n8n.example.com",
		"--tunnel", "homelab", "--service", "http://192.168.1.220:5678", "--no-tls-verify")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("--no-tls-verify sur une origine http est une erreur d'usage, got %v", err)
	}
}

// The same flag on an https:// origin must get past the usage checks. It then
// fails on credentials, which is the next gate and proves the flag itself was
// accepted.
func TestNoTLSVerifyIsAcceptedOnAnHTTPSOrigin(t *testing.T) {
	_, _, err := run(t, "cf", "route", "add", "pve.example.com",
		"--tunnel", "homelab", "--service", "https://192.168.1.23:8006", "--no-tls-verify")

	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == pve.ExitUsage {
		t.Errorf("--no-tls-verify sur une origine https doit passer la validation, got %v", err)
	}
}

// A service token and people cannot share one policy: the decision would have
// to be two things at once. Refusing is clearer than silently picking one.
func TestPolicyAddRefusesMixingPeopleAndAServiceToken(t *testing.T) {
	_, _, err := run(t, "cf", "access", "policy", "add", "--app", "pve.example.com",
		"--email", "moi@example.com", "--service-token", "cli")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("mélanger personnes et service token est une erreur d'usage, got %v", err)
	}
}

// A policy that includes nobody admits nobody. It is never what was meant.
func TestPolicyAddDemandsSomeoneToAdmit(t *testing.T) {
	_, _, err := run(t, "cf", "access", "policy", "add", "--app", "pve.example.com")

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitUsage {
		t.Errorf("une policy sans include est une erreur d'usage, got %v", err)
	}
}
