package pve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The rule of PRD §6.3 is "no endpoint written from memory". Documentation
// alone cannot enforce it; these two tests can.
//
// The first makes every call go through a declared endpoint. The second makes
// every declared endpoint appear in the map, with its source. Together they
// mean an undocumented endpoint cannot reach main.

// requestHelpers is every method that can put a path on the wire. Adding a new
// one — postMultipart and getRaw arrived with M7 — without adding it here would
// quietly open a door around the rule, so the list is checked below.
var requestHelpers = map[string]bool{
	"get": true, "do": true, "post": true, "del": true,
	"getRaw": true, "postMultipart": true, "write": true, "exchange": true,
}

// The list above must name every request helper the client actually has: a
// helper nobody watches is a helper through which an invented path can pass.
func TestRequestHelpersAreAllWatched(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "client.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			return true
		}
		// A helper is a method taking a path or an endpoint as its second
		// argument, right after the context.
		if len(fn.Type.Params.List) < 2 {
			return true
		}
		second := fn.Type.Params.List[1]
		name := ""
		switch t := second.Type.(type) {
		case *ast.Ident:
			name = t.Name
		}
		if name != "endpoint" && name != "string" {
			return true
		}
		if !requestHelpers[fn.Name.Name] {
			t.Errorf("%s: la méthode %q peut émettre une requête et n'est pas surveillée par TestNoInlineEndpoint",
				fset.Position(fn.Pos()), fn.Name.Name)
		}
		return true
	})
}

// No path may be written inline at a call site: paths live in endpoints.go.
func TestNoInlineEndpoint(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") ||
			name == "endpoints.go" {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !requestHelpers[sel.Sel.Name] {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, _ := strconv.Unquote(lit.Value)
				if strings.HasPrefix(value, "/") {
					t.Errorf("%s: chemin %q écrit en dur — déclare-le dans endpoints.go",
						fset.Position(lit.Pos()), value)
				}
			}
			return true
		})
	}
}

// documentedMethods reads docs/API-MAP.md and returns, per endpoint pattern,
// the set of methods the table actually documents for it.
//
// It parses the table rather than searching the whole file as a string,
// because a substring match answers the wrong question. Until PVX-085 this
// test only asked "does this pattern appear anywhere?", so a pattern already
// documented for ONE method silently vouched for every other — eight endpoints
// passed that way, `POST /nodes/{node}/status` among them. The rule of
// PRD §6.3 is per call, and a call is a method AND a path.
//
// The table is « | Endpoint | Méthode | … », and its method cell carries one or
// several methods separated by « · » when a single row documents a read and
// its write together.
func documentedMethods(t *testing.T) map[string]map[string]bool {
	t.Helper()

	raw, err := os.ReadFile("../../docs/API-MAP.md")
	if err != nil {
		t.Fatal(err)
	}

	documented := map[string]map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		pattern := strings.Trim(strings.TrimSpace(cells[1]), "`")
		if !strings.HasPrefix(pattern, "/") {
			// Header row, separator row, or a table that is not this one.
			continue
		}
		if documented[pattern] == nil {
			documented[pattern] = map[string]bool{}
		}
		for _, method := range strings.Split(cells[2], "·") {
			documented[pattern][strings.TrimSpace(method)] = true
		}
	}
	return documented
}

// Every declared endpoint must be documented FOR THE METHOD IT USES, with the
// source that was consulted to verify its schema.
func TestAPIMapCoverage(t *testing.T) {
	documented := documentedMethods(t)

	for _, e := range AllEndpoints {
		methods, ok := documented[e.Pattern]
		if !ok {
			t.Errorf("l'endpoint %s %s n'est pas dans docs/API-MAP.md — un endpoint non documenté est un endpoint qu'on a pu inventer",
				e.Method, e.Pattern)
			continue
		}
		if !methods[e.Method] {
			t.Errorf("docs/API-MAP.md documente %s, mais pas en %s — le schéma d'un POST n'est pas celui du GET de même chemin, et c'est le schéma qu'on est censé avoir vérifié",
				e.Pattern, e.Method)
		}
	}
}

// The tightening above is only worth its lines if it can FAIL. A coverage test
// that cannot distinguish two methods on one path is the one this replaced, and
// it passed for eight endpoints it had never really checked.
//
// So this pins the discrimination itself, against the real file: a method the
// table does not carry for a path must be seen as missing, even when the path
// is documented — including on a path that legitimately carries two methods.
func TestAPIMapCoverageDistinguishesMethods(t *testing.T) {
	documented := documentedMethods(t)

	for _, probe := range []struct {
		endpoint
		reason string
	}{
		{endpoint{"POST", "/version"}, "chemin documenté en GET seulement"},
		{endpoint{"DELETE", "/nodes/{node}/status"}, "chemin documenté en GET et POST, pas en DELETE"},
		{endpoint{"PUT", "/cluster/resources"}, "chemin documenté en GET seulement"},
	} {
		methods, ok := documented[probe.Pattern]
		if !ok {
			t.Fatalf("%s devrait être documenté pour au moins une méthode — la sonde ne teste plus rien", probe.Pattern)
		}
		if methods[probe.Method] {
			t.Errorf("%s %s est vu comme documenté (%s) — la couverture est retombée sur le motif seul",
				probe.Method, probe.Pattern, probe.reason)
		}
	}
}

// Placeholders are escaped, which is what saves paths carrying a UPID: those
// contain ':' and travel inside a path segment.
func TestEndpointPathEscapes(t *testing.T) {
	e := endpoint{"GET", "/nodes/{node}/tasks/{upid}/status"}

	got := e.Path("pve", "UPID:pve:0011A2:qmclone:9000:automation@pve:")
	want := "/nodes/pve/tasks/UPID:pve:0011A2:qmclone:9000:automation@pve:/status"

	// PathEscape leaves ':' alone inside a segment, which is legal per RFC 3986
	// — what matters is that '/' and '%' would be escaped.
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}

	if got := (endpoint{"GET", "/nodes/{node}"}).Path("a/b"); strings.Contains(got, "a/b") {
		t.Errorf("un '/' dans une valeur doit être échappé, got %q", got)
	}
}
