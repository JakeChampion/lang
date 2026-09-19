package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A nested `function` declaration is a source function the parser desugars to a
// bound lambda, which `try_lift_binding` hoists to `__lam_N`. From there it was
// indistinguishable from a lambda someone wrote, so it missed
// `desugar_lambda_returns` — the pre-pass that rewrites a return-position lambda
// to a binding the closure lift can box. Its return was hoisted to a bare
// address instead, and `semsource.ident` refuses a function-typed name (#9772).
//
// Each case pairs a program with the SAME program written at the top level.
// Where a function is written is not a fact about what the typed path can
// lower, so the two must produce the same number of declarations.
var nestedFnProductionCases = []struct {
	name   string
	nested string
	flat   string
	want   int
}{
	{"no-capture", `function mk(): i32 {
    function pick(b: i32): (i32) => i32 {
        return (x: i32) => x + 1;
    }
    var g: (i32) => i32 = pick(3);
    return g(4);
}
function main(): i32 { return mk(); }
`, `function pick(b: i32): (i32) => i32 { return (x: i32) => x + 1; }
function mk(): i32 { var g: (i32) => i32 = pick(3); return g(4); }
function main(): i32 { return mk(); }
`, 5},

	// The capturing form refused at `unsupported expression` rather than at the
	// closure-value leaf — a second histogram row with the same cause, so it is
	// pinned separately.
	{"capturing", `function mk(): i32 {
    function pick(b: i32): (i32) => i32 {
        return (x: i32) => x + b;
    }
    var g: (i32) => i32 = pick(3);
    return g(4);
}
function main(): i32 { return mk(); }
`, `function pick(b: i32): (i32) => i32 { return (x: i32) => x + b; }
function mk(): i32 { var g: (i32) => i32 = pick(3); return g(4); }
function main(): i32 { return mk(); }
`, 7},
}

// censusCount reads one `<label> <n>` line out of a census report.
func censusCount(t *testing.T, report, label string) int {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + label + `\s+(\d+)`).FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("no %q line in the report:\n%s", label, report)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// TestSelfHostNestedFnProduction is the gate: the nested spelling must reach
// the same production as the flat one, and neither may leave a refusal behind.
func TestSelfHostNestedFnProduction(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("census driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semsource_census_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	census := filepath.Join(dir, "census")
	build := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux",
		"-embed", stdlib, "-o", census, filepath.Join(dir, "semsource_census_run.fern"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the census failed: %v\n%s", err, out)
	}

	report := func(t *testing.T, name, src string) string {
		t.Helper()
		entry := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		out, err := exec.Command(census, entry).CombinedOutput()
		if err != nil {
			t.Fatalf("census of %s failed: %v\n%s", name, err, out)
		}
		return string(out)
	}

	for _, tc := range nestedFnProductionCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			nested := report(t, "nested", tc.nested)
			flat := report(t, "flat", tc.flat)
			t.Logf("nested:\n%s\nflat:\n%s", nested, flat)

			nm, np := censusCount(t, nested, "measured"), censusCount(t, nested, "produced")
			fm, fp := censusCount(t, flat, "measured"), censusCount(t, flat, "produced")
			if np != nm {
				t.Errorf("nested spelling produced %d of %d; the lifted `__lam_N` body's "+
					"return-position lambda was hoisted to a bare address instead of a box.\n%s",
					np, nm, nested)
			}
			if fp != fm {
				t.Errorf("flat spelling produced %d of %d — the control regressed, so the "+
					"nested/flat comparison below proves nothing.\n%s", fp, fm, flat)
			}
			if np != fp {
				t.Errorf("nested produced %d, flat produced %d: where the function is written "+
					"is not a fact about what the typed path can lower.", np, fp)
			}
		})
	}
}

// TestSelfHostNestedFnProductionRunsX86_64 pins both spellings to the same
// answer. It passes on either side of the fix — a refused module falls back to
// the AST lowering and runs anyway — and is here so the production gain cannot
// be bought with a miscompile.
func TestSelfHostNestedFnProductionRunsX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	run := func(t *testing.T, name, src string) int {
		t.Helper()
		progDir := t.TempDir()
		entry := filepath.Join(progDir, "main.fern")
		if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		asm := string(runDriverFile(t, runner, driverBin, entry))
		if !strings.Contains(asm, ".Lir") {
			t.Fatalf("%s did not route through the IR path", name)
		}
		bin := buildBin(t, gcc, progDir, "nested_fn_"+name, asm)
		_, exit := runBin(binCmd(runner, bin), "")
		return exit
	}

	for _, tc := range nestedFnProductionCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := run(t, "nested", tc.nested); got != tc.want {
				t.Errorf("nested exit = %d, want %d", got, tc.want)
			}
			if got := run(t, "flat", tc.flat); got != tc.want {
				t.Errorf("flat exit = %d, want %d", got, tc.want)
			}
		})
	}
}
