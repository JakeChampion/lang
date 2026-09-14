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

// TestSelfHostSemanticSourceCensusLoadsTheStdlib pins the one property the
// coverage census (examples/self_host/semsource_census_run.fern) cannot be
// trusted without: that the program it measures is the WHOLE program.
//
// The loader drops an import it cannot resolve in silence, which is right in a
// compiler driver — an intrinsic looks exactly like an unresolvable import —
// and is the census's worst failure mode. Run without the stdlib overlay, an
// entry that imports std/test measured its own 33 declarations, refused every
// call into the missing module, and charged each refusal to whatever leaf the
// unresolved result landed on. The histogram that came out was dominated by a
// class with no construct behind it at all.
//
// So this gate builds the census the way it is meant to be built, runs it over
// an entry whose declarations are nearly all in the stdlib, and asserts the
// count is the closure's rather than the entry's.
func TestSelfHostSemanticSourceCensusLoadsTheStdlib(t *testing.T) {
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
	bin := filepath.Join(dir, "census")
	// Shelled out rather than run through buildSelfHostBin's cache for the
	// reason buildPlaygroundDriver gives: that path cannot pass an asset
	// bundle, and the bundle is the whole point here.
	build := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux",
		"-embed", stdlib, "-o", bin, filepath.Join(dir, "semsource_census_run.fern"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the census failed: %v\n%s", err, out)
	}

	// An entry that is almost entirely stdlib: a handful of declarations of its
	// own, reaching a module tree of thousands.
	entry := filepath.Join(dir, "entry.fern")
	writeEntry(t, entry, `import "std/test";
function t_one(): test.TestOutcome { return test.assert_eq(1, 1); }
function main(): i32 {
    var r: test.TestRunner = test.test_new("census");
    r = r.it("one", t_one());
    return r.finish();
}
`)
	out, err := exec.Command(bin, entry).CombinedOutput()
	if err != nil {
		t.Fatalf("census failed: %v\n%s", err, out)
	}
	report := string(out)
	t.Logf("census of a two-declaration entry over std/test:\n%s", report)

	count := func(label string) int {
		m := regexp.MustCompile(`(?m)^` + label + `\s+(\d+)`).FindStringSubmatch(report)
		if m == nil {
			t.Fatalf("no %q line in the report:\n%s", label, report)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	// The entry declares two functions. Anything near that means the import was
	// dropped and the census is measuring the entry alone.
	total := count("functions")
	if total < 100 {
		t.Fatalf("census counted %d functions for an entry that imports std/test: the stdlib "+
			"was not loaded, so this measures the entry alone and every call into the missing "+
			"module refuses under a reason that names the binding rather than the gap.\n%s",
			total, report)
	}
	if lowered := count("lowered"); lowered == 0 {
		t.Errorf("census lowered 0 of %d functions; report:\n%s", total, report)
	}

	// An import that still does not resolve has to be named, not dropped.
	orphan := filepath.Join(dir, "orphan.fern")
	writeEntry(t, orphan, `import "std/definitely_not_a_module";
function main(): i32 { return 0; }
`)
	out, err = exec.Command(bin, orphan).CombinedOutput()
	if err == nil {
		t.Fatalf("census exited 0 on an unresolvable import; it must refuse to report a "+
			"partial program:\n%s", out)
	}
	if !strings.Contains(string(out), "unresolved import: std/definitely_not_a_module") {
		t.Errorf("census did not name the unresolved import:\n%s", out)
	}
}

func writeEntry(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
