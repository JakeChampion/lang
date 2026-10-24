package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const semanticsIndexPath = "../../../spec/semantics.md"

const (
	noPin   = "—"
	freedom = "n/a — freedom"
)

type semanticClaim struct {
	id   string
	doc  string
	rule string
	pin  string
}

var (
	claimRowRe   = regexp.MustCompile("^\\| `[A-Z]{2}-\\d{2}` \\|")
	claimIDRe    = regexp.MustCompile("^`([A-Z]{2}-\\d{2})`$")
	specMarkRe   = regexp.MustCompile(`^//\s*spec:\s*([A-Z]{2}-\d{2})\s*$`)
	claimCountRe = regexp.MustCompile(`\*\*(\d+) of (\d+)\*\* claims are pinned`)
	freedomRe    = regexp.MustCompile(`\*\*(\w+) are freedoms\*\*`)
)

func readSemanticsIndex(t *testing.T) (string, []semanticClaim) {
	t.Helper()
	raw, err := os.ReadFile(semanticsIndexPath)
	if err != nil {
		t.Fatalf("read %s: %v", semanticsIndexPath, err)
	}
	var claims []semanticClaim
	for _, line := range strings.Split(string(raw), "\n") {
		if !claimRowRe.MatchString(line) {
			continue
		}
		cells := splitTableRow(line)
		if len(cells) != 4 {
			t.Errorf("claim row has %d cells, want 4: %s", len(cells), line)
			continue
		}
		m := claimIDRe.FindStringSubmatch(cells[0])
		if m == nil {
			t.Errorf("claim row has a malformed ID cell %q", cells[0])
			continue
		}
		claims = append(claims, semanticClaim{
			id:   m[1],
			doc:  strings.Trim(cells[1], "`"),
			rule: cells[2],
			pin:  strings.Trim(cells[3], "`"),
		})
	}
	if len(claims) == 0 {
		t.Fatalf("%s has no claim rows — the index parse is broken, not the index", semanticsIndexPath)
	}
	return string(raw), claims
}

func splitTableRow(line string) []string {
	const escaped = "\x00"
	line = strings.ReplaceAll(line, `\|`, escaped)
	line = strings.Trim(line, "|")
	var out []string
	for _, cell := range strings.Split(line, "|") {
		out = append(out, strings.TrimSpace(strings.ReplaceAll(cell, escaped, "|")))
	}
	return out
}

func specMarkers(t *testing.T, caseDir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(caseDir, "main.fern"))
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if m := specMarkRe.FindStringSubmatch(line); m != nil {
			ids = append(ids, m[1])
			continue
		}
		if !strings.HasPrefix(line, "//") && line != "" {
			break
		}
	}
	return ids
}

func TestSemanticsIndexIsAccurate(t *testing.T) {
	raw, claims := readSemanticsIndex(t)

	seen := map[string]bool{}
	pinnedBy := map[string][]string{}
	var pinned, freedoms int

	for _, c := range claims {
		if seen[c.id] {
			t.Errorf("%s is listed twice; a claim ID has to identify one rule", c.id)
		}
		seen[c.id] = true

		if _, err := os.Stat("../../../" + c.doc); err != nil {
			t.Errorf("%s cites %s, which does not exist", c.id, c.doc)
		}
		if strings.TrimSpace(c.rule) == "" {
			t.Errorf("%s states no rule", c.id)
		}

		switch c.pin {
		case freedom:
			freedoms++
		case noPin:
		default:
			pinned++
			dir := filepath.Join(conformanceCases, c.pin)
			if _, err := os.Stat(filepath.Join(dir, "main.fern")); err != nil {
				t.Errorf("%s is pinned by %q, which is not a conformance case", c.id, c.pin)
				continue
			}
			markers := specMarkers(t, dir)
			if !slices.Contains(markers, c.id) {
				t.Errorf("%s claims to be pinned by %q, but that case's header carries no `// spec: %s` "+
					"marker (it declares %v) — so nothing stops the case being rewritten to be about "+
					"something else", c.id, c.pin, c.id, markers)
			}
			pinnedBy[c.id] = append(pinnedBy[c.id], c.pin)
		}
	}

	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatalf("read %s: %v", conformanceCases, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, id := range specMarkers(t, filepath.Join(conformanceCases, e.Name())) {
			if !seen[id] {
				t.Errorf("case %q declares `// spec: %s`, which is not a claim in %s",
					e.Name(), id, semanticsIndexPath)
			}
		}
	}

	docs := map[string]bool{}
	for _, c := range claims {
		docs[c.doc] = true
	}
	for _, d := range normativeDocsFromReadme(t) {
		if !docs[d] {
			t.Errorf("spec/README.md lists %s as normative prose, but %s extracts no claim from it",
				d, semanticsIndexPath)
		}
	}

	if m := claimCountRe.FindStringSubmatch(raw); m == nil {
		t.Errorf("%s no longer states how many claims are pinned", semanticsIndexPath)
	} else {
		wantPinned, _ := strconv.Atoi(m[1])
		wantTotal, _ := strconv.Atoi(m[2])
		if wantPinned != pinned || wantTotal != len(claims) {
			t.Errorf("%s says %d of %d claims are pinned; the table has %d of %d",
				semanticsIndexPath, wantPinned, wantTotal, pinned, len(claims))
		}
	}
	if m := freedomRe.FindStringSubmatch(raw); m == nil {
		t.Errorf("%s no longer says how many claims are freedoms", semanticsIndexPath)
	} else if got := numberWord(m[1]); got != freedoms {
		t.Errorf("%s says %s claims are freedoms; the table has %d", semanticsIndexPath, m[1], freedoms)
	}

	t.Logf("%d claims: %d pinned, %d freedoms, %d gaps",
		len(claims), pinned, freedoms, len(claims)-pinned-freedoms)
}

func normativeDocsFromReadme(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../../spec/README.md")
	if err != nil {
		t.Fatalf("read spec/README.md: %v", err)
	}
	re := regexp.MustCompile("^\\| `(docs/[A-Z-]+\\.md)` \\|")
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatal("spec/README.md's normative-prose table has no rows — the parse is broken")
	}
	sort.Strings(out)
	return out
}

func numberWord(w string) int {
	words := []string{"zero", "one", "two", "three", "four", "five",
		"six", "seven", "eight", "nine", "ten"}
	for i, s := range words {
		if strings.EqualFold(w, s) {
			return i
		}
	}
	n, err := strconv.Atoi(w)
	if err != nil {
		return -1
	}
	return n
}

func TestSemanticClaimPrefixesMatchTheirDoc(t *testing.T) {
	_, claims := readSemanticsIndex(t)
	prefixOf := map[string]string{}
	for _, c := range claims {
		p := c.id[:2]
		if prev, ok := prefixOf[p]; ok && prev != c.doc {
			t.Errorf("prefix %s is used for both %s and %s", p, prev, c.doc)
		}
		prefixOf[p] = c.doc
	}
	docPrefix := map[string]string{}
	for p, d := range prefixOf {
		if prev, ok := docPrefix[d]; ok {
			t.Errorf("%s claims use two prefixes, %s and %s", d, prev, p)
		}
		docPrefix[d] = p
	}
	if len(prefixOf) == 0 {
		t.Fatal("no prefixes found")
	}
}
