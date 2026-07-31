package iac

import (
	"os"
	"path/filepath"
	"testing"
)

func writeOut(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A playbook with no service role publishes nothing, and that is a run, not a
// failure.
func TestReadOutputsToleratesNothingPublished(t *testing.T) {
	got, err := ReadOutputs(t.TempDir())
	if err != nil {
		t.Fatalf("ReadOutputs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadOutputs = %v, attendu vide", got)
	}
	if _, err := ReadOutputs(filepath.Join(t.TempDir(), "jamais-créé")); err != nil {
		t.Errorf("un dossier absent ne doit pas être une erreur : %v", err)
	}
}

func TestReadOutputsMergesTheServicesOfOneHost(t *testing.T) {
	dir := t.TempDir()
	writeOut(t, dir, "app-01.docker.json", `{"docker":"27.3.1"}`)
	writeOut(t, dir, "app-01.postgresql.json", `{"postgresql.user":"app","postgresql.database":"app"}`)

	got, err := ReadOutputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("ReadOutputs = %v, un seul hôte attendu", got)
	}
	if len(got[0].Values) != 3 {
		t.Errorf("valeurs = %v, les deux services doivent être fusionnés", got[0].Values)
	}
	if got[0].Values["docker"] != "27.3.1" {
		t.Errorf("docker = %q", got[0].Values["docker"])
	}
}

func TestReadOutputsSortsHostsAndSeparatesThem(t *testing.T) {
	dir := t.TempDir()
	writeOut(t, dir, "b-02.docker.json", `{"docker":"27"}`)
	writeOut(t, dir, "a-01.docker.json", `{"docker":"26"}`)

	got, err := ReadOutputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Host != "a-01" || got[1].Host != "b-02" {
		t.Fatalf("ReadOutputs = %v, attendu deux hôtes triés", got)
	}
	if got[0].Values["docker"] != "26" {
		t.Error("les valeurs de deux hôtes ne doivent pas se mélanger")
	}
}

// A hostname may contain dots; a service id may not. The split is done from the
// right for exactly that reason.
func TestReadOutputsSplitsTheServiceFromTheRight(t *testing.T) {
	dir := t.TempDir()
	writeOut(t, dir, "app.lab.internal.docker.json", `{"docker":"27"}`)

	got, err := ReadOutputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != "app.lab.internal" {
		t.Errorf("hôte = %v, attendu « app.lab.internal »", got)
	}
}

// A corrupt file is reported, never skipped: a silently missing password reads
// as "this service has no credentials".
func TestReadOutputsRefusesACorruptFile(t *testing.T) {
	dir := t.TempDir()
	writeOut(t, dir, "app-01.docker.json", `pas du json`)

	if _, err := ReadOutputs(dir); err == nil {
		t.Error("une sortie illisible doit être signalée")
	}
}
