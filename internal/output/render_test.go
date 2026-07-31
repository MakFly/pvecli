package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

var sample = Rows{
	Headers: []string{"NOM", "STATUT", "RAM"},
	Cells: [][]string{
		{"pve", "online", "2.0 GiB / 30.8 GiB"},
		{"pve2", "offline", "0 B / 16.0 GiB"},
	},
}

// Golden: a fixture in, an expected table out, byte for byte. Not a terminal,
// so columns are separated by a single tab — which is what `cut -f` expects.
func TestTableGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Options{Format: Table}, nil, sample); err != nil {
		t.Fatal(err)
	}

	want := "NOM\tSTATUT\tRAM\n" +
		"pve\tonline\t2.0 GiB / 30.8 GiB\n" +
		"pve2\toffline\t0 B / 16.0 GiB\n"

	if got := buf.String(); got != want {
		t.Errorf("sortie table :\n%q\nattendu :\n%q", got, want)
	}
}

func TestNoHeaders(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Options{Format: Table, NoHeaders: true}, nil, sample); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "NOM") {
		t.Errorf("--no-headers doit supprimer l'en-tête:\n%s", buf.String())
	}
}

func TestColumnsSelectAndReorder(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, Options{Format: Table, Columns: []string{"ram", "nom"}}, nil, sample)
	if err != nil {
		t.Fatal(err)
	}

	want := "RAM\tNOM\n" +
		"2.0 GiB / 30.8 GiB\tpve\n" +
		"0 B / 16.0 GiB\tpve2\n"

	if got := buf.String(); got != want {
		t.Errorf("--columns :\n%q\nattendu :\n%q", got, want)
	}
}

// An unknown column is a typo, and the error lists what exists.
func TestUnknownColumn(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, Options{Format: Table, Columns: []string{"nomm"}}, nil, sample)

	if err == nil {
		t.Fatal("une colonne inconnue doit échouer")
	}
	if !strings.Contains(err.Error(), "STATUT") {
		t.Errorf("l'erreur doit lister les colonnes disponibles: %v", err)
	}
}

// The JSON output is the typed value itself, not a home-made wrapper: `jq`
// must see the field names the node returned.
func TestJSONIsTheRawTypedValue(t *testing.T) {
	type node struct {
		Node   string `json:"node"`
		MaxMem int64  `json:"maxmem"`
	}
	data := []node{{Node: "pve", MaxMem: 33041162240}}

	var buf bytes.Buffer
	if err := Render(&buf, Options{Format: JSON}, data, Rows{}); err != nil {
		t.Fatal(err)
	}

	var back []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("la sortie n'est pas un tableau JSON: %v\n%s", err, buf.String())
	}
	if back[0]["node"] != "pve" {
		t.Errorf("jq '.[0].node' ne trouverait rien: %s", buf.String())
	}
}

func TestYAML(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]string{"node": "pve"}
	if err := Render(&buf, Options{Format: YAML}, data, Rows{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "node: pve") {
		t.Errorf("sortie YAML inattendue: %q", buf.String())
	}
}

func TestParseFormat(t *testing.T) {
	for _, in := range []string{"table", "JSON", "yaml"} {
		if _, err := ParseFormat(in); err != nil {
			t.Errorf("ParseFormat(%q): %v", in, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("un format inconnu doit être refusé")
	}
}
