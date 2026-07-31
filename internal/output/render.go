package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	yaml "go.yaml.in/yaml/v3"
	"golang.org/x/term"
)

// Format is how a result is written.
type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
	YAML  Format = "yaml"
)

// ParseFormat validates the --output value.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case Table:
		return Table, nil
	case JSON:
		return JSON, nil
	case YAML:
		return YAML, nil
	default:
		return "", fmt.Errorf("format de sortie %q inconnu — attendu table, json ou yaml", s)
	}
}

// Options configures a rendering.
type Options struct {
	Format    Format
	NoHeaders bool
	// Columns restricts and orders the table columns, by header name.
	Columns []string
}

// Rows is a table projection: a header line and the cells under it.
type Rows struct {
	Headers []string
	Cells   [][]string
}

// Render writes data.
//
// json and yaml serialise `data` itself — the typed API value, with no
// home-made wrapper around it — so that `| jq '.[].name'` works without
// detours. That is the whole difference between a scriptable CLI and a
// decorative one; the table is only the human projection of the same thing.
func Render(w io.Writer, o Options, data any, rows Rows) error {
	switch o.Format {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(data)

	case YAML:
		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err

	default:
		return writeTable(w, o, rows)
	}
}

func writeTable(w io.Writer, o Options, rows Rows) error {
	headers, cells, err := selectColumns(o.Columns, rows)
	if err != nil {
		return err
	}

	// Padding is for eyes. When stdout is not a terminal the output is being
	// read by `cut`, `awk` or a diff, and single tabs are what those expect.
	if !IsTerminal(w) {
		if !o.NoHeaders {
			if _, err := fmt.Fprintln(w, strings.Join(headers, "\t")); err != nil {
				return err
			}
		}
		for _, row := range cells {
			if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
				return err
			}
		}
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if !o.NoHeaders {
		_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))
	}
	for _, row := range cells {
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

// selectColumns keeps only the requested columns, in the requested order.
func selectColumns(want []string, rows Rows) ([]string, [][]string, error) {
	if len(want) == 0 {
		return rows.Headers, rows.Cells, nil
	}

	index := make(map[string]int, len(rows.Headers))
	for i, h := range rows.Headers {
		index[strings.ToLower(h)] = i
	}

	picked := make([]int, 0, len(want))
	headers := make([]string, 0, len(want))
	for _, name := range want {
		i, ok := index[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return nil, nil, fmt.Errorf("colonne %q inconnue — colonnes disponibles : %s",
				name, strings.Join(rows.Headers, ", "))
		}
		picked = append(picked, i)
		headers = append(headers, rows.Headers[i])
	}

	cells := make([][]string, 0, len(rows.Cells))
	for _, row := range rows.Cells {
		out := make([]string, 0, len(picked))
		for _, i := range picked {
			if i < len(row) {
				out = append(out, row[i])
			} else {
				out = append(out, "")
			}
		}
		cells = append(cells, out)
	}
	return headers, cells, nil
}

// IsTerminal reports whether w is a terminal. Anything that is not an *os.File
// — a bytes.Buffer in a test, a pipe — is not.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
