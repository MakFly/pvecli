package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The front matter is the contract with Claude Code: it discovers the agent by
// reading these keys. A definition that loses its `name:` is silently ignored —
// installed, present on disk, and never invoked.
func TestEmbeddedAgentCarriesItsFrontMatter(t *testing.T) {
	if !strings.HasPrefix(proxmoxAgent, "---\n") {
		t.Fatal("la définition ne commence pas par un bloc de front matter")
	}
	end := strings.Index(proxmoxAgent[4:], "\n---\n")
	if end < 0 {
		t.Fatal("bloc de front matter non refermé")
	}
	head := proxmoxAgent[4 : 4+end]

	for _, key := range []string{"name:", "description:", "tools:", "model:"} {
		if !strings.Contains(head, key) {
			t.Errorf("front matter sans %q", key)
		}
	}
	// The file name is how Claude Code addresses the agent; a mismatch with
	// `name:` produces an agent nobody can call by the name it announces.
	if want := "name: " + strings.TrimSuffix(agentFileName, ".md"); !strings.Contains(head, want) {
		t.Errorf("le nom déclaré ne correspond pas à %s (attendu %q)", agentFileName, want)
	}
}

// The agent's whole value is knowing the rules that are not in --help. If one
// of these disappears, the agent still installs and still answers — it just
// answers like any assistant that never saw this lab.
func TestEmbeddedAgentKeepsTheRulesThatCostSomething(t *testing.T) {
	for _, must := range []string{
		"8192",             // mebibytes, not gigabytes
		"qemu-guest-agent", // the twelve-minute apply
		"managed",          // Terraform's ownership guard
		"900-999",          // the reserved integration range
		"Sys.Modify",       // the deliberate refusal
		"--insecure",       // never as a workaround
	} {
		if !strings.Contains(proxmoxAgent, must) {
			t.Errorf("la définition ne mentionne plus %q", must)
		}
	}
}

func TestAIPrintEmitsExactlyWhatIsEmbedded(t *testing.T) {
	stdout, _, err := run(t, "ai", "print")
	if err != nil {
		t.Fatalf("ai print: %v", err)
	}
	if stdout != proxmoxAgent {
		t.Error("« ai print » ne rend pas la définition embarquée à l'octet près")
	}
}

func TestAIInstallWritesThenReportsItselfCurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, agentFileName)

	stdout, _, err := run(t, "ai", "install", "--dir", dir)
	if err != nil {
		t.Fatalf("ai install: %v", err)
	}
	if !strings.Contains(stdout, target) {
		t.Errorf("l'installation ne dit pas où elle a écrit : %q", stdout)
	}

	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("le fichier n'a pas été écrit : %v", err)
	}
	if string(raw) != proxmoxAgent {
		t.Error("le fichier écrit diffère de la définition embarquée")
	}

	// Re-installing is a no-op, not a rewrite: an install that always claims to
	// have done something teaches you to stop reading it.
	stdout, _, err = run(t, "ai", "install", "--dir", dir)
	if err != nil {
		t.Fatalf("seconde installation : %v", err)
	}
	if !strings.Contains(stdout, "à jour") {
		t.Errorf("la seconde installation devait se dire à jour : %q", stdout)
	}
}

// The refusal is the point of the command: a locally edited agent is either a
// customisation or an older version, and both are worth a look before loss.
func TestAIInstallRefusesToClobberALocalEdit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, agentFileName)
	if err := os.WriteFile(target, []byte("--- ma version à moi ---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "ai", "install", "--dir", dir)
	if err == nil {
		t.Fatal("une définition modifiée doit faire échouer l'installation")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("l'erreur n'indique pas la sortie : %v", err)
	}

	raw, _ := os.ReadFile(target)
	if string(raw) != "--- ma version à moi ---\n" {
		t.Error("le fichier a été touché malgré le refus")
	}

	if _, _, err := run(t, "ai", "install", "--dir", dir, "--force"); err != nil {
		t.Fatalf("--force doit trancher : %v", err)
	}
	raw, _ = os.ReadFile(target)
	if string(raw) != proxmoxAgent {
		t.Error("--force n'a pas remplacé le fichier")
	}
}

func TestAIStatusNamesTheThreeStates(t *testing.T) {
	dir := t.TempDir()

	stdout, _, err := run(t, "ai", "status", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "absent") {
		t.Errorf("état attendu « absent » : %q", stdout)
	}

	if _, _, err := run(t, "ai", "install", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if stdout, _, err = run(t, "ai", "status", "--dir", dir); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(stdout, "à jour") {
		t.Errorf("état attendu « à jour » : %q", stdout)
	}

	if err := os.WriteFile(filepath.Join(dir, agentFileName), []byte("autre chose"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stdout, _, err = run(t, "ai", "status", "--dir", dir); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(stdout, "diffère") {
		t.Errorf("état attendu « diffère » : %q", stdout)
	}
}

// A moved Claude Code configuration must be followed, or the agent lands where
// nothing reads it — an installation that succeeds and does nothing.
func TestAgentDirFollowsClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/ailleurs/.claude")

	got, err := agentDir("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/ailleurs/.claude", "agents"); got != want {
		t.Errorf("agentDir = %q, want %q", got, want)
	}

	// An explicit --dir still wins over the environment.
	if got, _ = agentDir("/tmp/x"); got != "/tmp/x" {
		t.Errorf("--dir ignoré : %q", got)
	}
}
