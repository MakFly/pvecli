package cf

import (
	"os"
	"strings"
	"testing"
)

const apiMap = "../../docs/CF-API-MAP.md"

// The rule of PRD §6.3, applied to the second API this tool speaks: an endpoint
// nobody documented is an endpoint someone wrote from memory.
func TestEveryEndpointIsDocumented(t *testing.T) {
	raw, err := os.ReadFile(apiMap)
	if err != nil {
		t.Fatalf("lecture de %s : %v", apiMap, err)
	}
	doc := string(raw)

	for _, e := range AllEndpoints {
		// The doc writes the pattern in a code span, and the method in its own
		// cell: both have to be on the same row.
		found := false
		for _, line := range strings.Split(doc, "\n") {
			if strings.Contains(line, "`"+e.Pattern+"`") && strings.Contains(line, "| "+e.Method+" ") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s %s n'est pas documenté dans %s", e.Method, e.Pattern, apiMap)
		}
	}
}

// An entry documenting an endpoint the client no longer has is an entry that
// has stopped describing anything -- and it would silently absolve whatever
// path takes that shape next.
func TestEveryDocumentedRowMatchesAnEndpoint(t *testing.T) {
	raw, err := os.ReadFile(apiMap)
	if err != nil {
		t.Fatal(err)
	}

	known := map[string]bool{}
	for _, e := range AllEndpoints {
		known[e.Method+" "+e.Pattern] = true
	}

	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "| `/") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		pattern := strings.Trim(strings.TrimSpace(cells[1]), "`")
		method := strings.TrimSpace(cells[2])
		if !known[method+" "+pattern] {
			t.Errorf("%s %s est documenté mais n'existe pas dans AllEndpoints", method, pattern)
		}
	}
}
