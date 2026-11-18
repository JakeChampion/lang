package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// manyJoinsSrc matches twenty spliced probes in one function. Each probe
// gives a join of a constant tag and a string payload, so semthread has more
// joins to split than it once had rounds for.
func manyJoinsSrc() string {
	var b strings.Builder
	b.WriteString("enum Probe { Has(string), Not }\n")
	b.WriteString("function probe(i: i32): Probe {\n    if (i % 3 == 0) { return Not; }\n    return Has(i.to_string());\n}\n")
	b.WriteString("@noinline function many(r: i32): i32 {\n    let acc: i32 = 0;\n")
	for k := 0; k < 20; k++ {
		fmt.Fprintf(&b, "    match (probe(r + %d)) { Has(s) => { acc = acc + s.len() * %d; }, Not => { acc = acc + %d; } }\n", k, k+1, k)
	}
	b.WriteString("    return acc;\n}\n")
	b.WriteString("function main(): i32 {\n    return many(7) % 100;\n}\n")
	return b.String()
}

// Each join is split, so each payload goes straight to the arm that reads it,
// and the certifier proves every value of many. A join left unsplit keeps a
// phi of the payload that the certifier cannot connect to its tag.
func TestSelfHostSemthreadSplitsEveryConstantJoin(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(manyJoinsSrc()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := compileCapturing(t, cli, src, "FERN_RC_CERTIFY=report", "FERN_RC_CERTIFY_MEMO=0")
	if code != 0 {
		t.Fatalf("report compile: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "stopped after") {
		t.Fatalf("a pass did not settle:\n%s", out)
	}
	line := regexp.MustCompile(`(?m)^FERN_RC_CERTIFY: census many tracked=\d+ unproved=(\d+) `).FindStringSubmatch(out)
	if line == nil {
		t.Fatalf("no census line for many:\n%s", out)
	}
	if line[1] != "0" {
		t.Fatalf("many has %s unproved value(s), want 0:\n%s", line[1], out)
	}
}
