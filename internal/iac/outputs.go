package iac

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OutputDirVar is the extra-var the roles read to know where to publish. Empty
// means "publish nothing", which is what a run outside `iac configure` gets.
const OutputDirVar = "pvecli_out_dir"

// Outputs are the connection details one host's services reported.
//
// Proxmox knows a MAC on a bridge; it has no idea that a Postgres role exists
// inside the guest, let alone which database it created. Only the run that
// installed the service knows, so the roles write it down on the controller and
// this reads it back. Nothing here is inferred.
type Outputs struct {
	Host   string
	Values map[string]string
}

// ReadOutputs collects every `<host>.<service>.json` a playbook run published.
//
// A missing directory or an empty one is not an error: a playbook with no
// service role publishes nothing, and that is a legitimate run.
func ReadOutputs(dir string) ([]Outputs, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lecture des sorties dans %s : %w", dir, err)
	}

	byHost := map[string]map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		// "<host>.<service>.json" — the host may contain dots, the service may
		// not, so the split is done from the right.
		trimmed := strings.TrimSuffix(name, ".json")
		dot := strings.LastIndex(trimmed, ".")
		if dot <= 0 {
			continue
		}
		host := trimmed[:dot]

		raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // path listed from dir
		if err != nil {
			return nil, fmt.Errorf("lecture de %s : %w", name, err)
		}
		var values map[string]string
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("sortie %s illisible : %w", name, err)
		}
		if byHost[host] == nil {
			byHost[host] = map[string]string{}
		}
		for k, v := range values {
			byHost[host][k] = v
		}
	}

	out := make([]Outputs, 0, len(byHost))
	for host, values := range byHost {
		out = append(out, Outputs{Host: host, Values: values})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}
