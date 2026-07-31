package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	yaml "go.yaml.in/yaml/v3"
)

// Path returns where the configuration file lives, in decreasing priority:
// the --config flag, $PVECLI_CONFIG, $XDG_CONFIG_HOME/pvecli/, ~/.config/pvecli/.
func Path(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if p := os.Getenv("PVECLI_CONFIG"); p != "" {
		return p, nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "pvecli", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("impossible de déterminer le dossier personnel : %w", err)
	}
	return filepath.Join(home, ".config", "pvecli", "config.yaml"), nil
}

// Load reads the configuration file.
//
// A missing file is not an error: it yields an empty File, so that environment
// variables and flags alone can drive the CLI — which is what a CI job or a
// one-off `--endpoint` invocation does.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{Contexts: map[string]*Context{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", path, err)
	}

	// Walk the raw document before decoding it. The typed structs below have
	// no token_secret field, so an unmarshal would drop the key silently —
	// the secret would stay on disk, unnoticed, which is the exact failure
	// this check exists to prevent.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s : YAML invalide : %w", path, err)
	}
	if line := findKey(&doc, "token_secret"); line > 0 {
		return nil, refuseTokenSecret(fmt.Sprintf("le fichier %s contient « token_secret » (ligne %d).", path, line))
	}

	var f File
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s : YAML invalide : %w", path, err)
	}
	if f.Contexts == nil {
		f.Contexts = map[string]*Context{}
	}
	return &f, nil
}

// findKey returns the line of the first mapping key named name, at any depth,
// or 0 if there is none. Depth matters: the key is refused wherever it sits,
// including in a context that is not the current one.
func findKey(n *yaml.Node, name string) int {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == name {
				return n.Content[i].Line
			}
		}
	}
	for _, child := range n.Content {
		if line := findKey(child, name); line > 0 {
			return line
		}
	}
	return 0
}

// Save writes the file with mode 0600, creating its directory with 0700.
//
// The chmod is not redundant with WriteFile: WriteFile only applies the mode
// when it creates the file, so an already-existing 0644 config would keep its
// permissions forever.
func Save(path string, f *File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("création de %s : %w", filepath.Dir(path), err)
	}
	out, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("écriture de %s : %w", path, err)
	}
	return os.Chmod(path, 0o600)
}
