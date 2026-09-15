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

	// A template nothing instantiates is counted apart from a refusal. It has
	// no body, so a measurement over it is not evidence about what the
	// boundary admits, and counting it as a refusal made it 44% of the
	// corpus histogram.
	shaken := filepath.Join(dir, "shaken.fern")
	writeEntry(t, shaken, `function used[T](x: T[]): i32 { return x.len(); }
function never_asked[T](x: T[], y: T): i32 { return x.len(); }
function main(): i32 { return used([1, 2]); }
`)
	out, err = exec.Command(bin, shaken).CombinedOutput()
	if err != nil {
		t.Fatalf("census failed: %v\n%s", err, out)
	}
	shakenReport := string(out)
	t.Logf("census of an entry with one uninstantiated template:\n%s", shakenReport)
	countIn := func(report, label string) int {
		m := regexp.MustCompile(`(?m)^` + label + `\s+(\d+)`).FindStringSubmatch(report)
		if m == nil {
			t.Fatalf("no %q line in the report:\n%s", label, report)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if n := countIn(shakenReport, "bodyless"); n != 1 {
		t.Errorf("bodyless = %d, want 1: never_asked is a template nothing instantiates, so it has "+
			"no body to lower and must not be counted as a refusal.\n%s", n, shakenReport)
	}
	if got, want := countIn(shakenReport, "measured"), countIn(shakenReport, "functions")-1; got != want {
		t.Errorf("measured = %d, want %d (functions minus the bodyless template)\n%s", got, want, shakenReport)
	}
	if strings.Contains(shakenReport, "uninstantiated generic") {
		t.Errorf("an uninstantiated template still appears in the refusal histogram; it is not a "+
			"refusal, it is a declaration with nothing to measure.\n%s", shakenReport)
	}

	// The front end's own enums are injected as declarations before lowering,
	// and the census has to do it too. Without the injection a module that
	// names one finds no variants for it, union_layout answers 0, and every
	// declaration touching it refuses under a name that describes the enum
	// rather than the miss: IoError is the error arm of every filesystem
	// Result, so that was 10,336 refusals across the corpus with no construct
	// behind them. Same failure mode as the dropped-overlay case above, and
	// the second time the instrument has understated coverage this way.
	injected := filepath.Join(dir, "injected.fern")
	writeEntry(t, injected, `function code(e: IoError): i32 {
    match (e) {
        NotFound(p) => { return 1; },
        PermissionDenied(p) => { return 2; },
        AlreadyExists(p) => { return 3; },
        InvalidUtf8(p) => { return 4; },
        Interrupted => { return 5; },
        Unsupported => { return 6; },
        Other(p, m) => { return 7; }
    }
    return 0;
}
function main(): i32 { return code(Interrupted); }
`)
	out, err = exec.Command(bin, injected).CombinedOutput()
	if err != nil {
		t.Fatalf("census failed on an entry using a builtin enum: %v\n%s", err, out)
	}
	injectedReport := string(out)
	t.Logf("census of an entry matching on IoError:\n%s", injectedReport)
	for _, refusal := range []string{"unsupported enum type", "unsupported match scrutinee"} {
		if strings.Contains(injectedReport, refusal) {
			t.Errorf("census reports %q for a match on IoError: the front end's enums are not "+
				"injected, so the boundary sees an undeclared union and the refusal is the "+
				"instrument's rather than the boundary's.\n%s", refusal, injectedReport)
		}
	}
	if got, want := countIn(injectedReport, "lowered"), countIn(injectedReport, "measured"); got != want {
		t.Errorf("lowered = %d of %d measured; a match over IoError is ordinary variant code and "+
			"must lower whole.\n%s", got, want, injectedReport)
	}

	// std/string's segment producers hand every piece to an array whose
	// declared element type is an OWNED string. A bare slice there is a view
	// of the receiver, which the array outlives, so the boundary refuses it —
	// correctly, and that refusal was the largest class in the corpus. The
	// producers now materialise each piece the way their runtime twins in
	// asmcore.fern already do, so nothing in this family escapes.
	// std/http is named alongside std/string: http_path_segments carries one of
	// the family's sites and lives outside std/string's module tree, so an
	// entry over the six methods alone would leave it unpinned.
	//
	// std/regex's three sites CANNOT be pinned here and are deliberately not
	// claimed. regex_split refuses before the boundary ever reaches its
	// appends — __rx_compile and __rx_search_from have no semantic contract —
	// so reverting its materialisation leaves the census reporting nothing,
	// which a gate naming it would read as green. It becomes pinnable when
	// those intrinsics get contracts, and not before.
	segments := filepath.Join(dir, "segments.fern")
	writeEntry(t, segments, "import \"std/string\";\nimport \"std/http\";\n"+
		"function main(): i32 {\n"+
		"    var s: string = \"a b\" + \" c\";\n"+
		"    return s.fields().len() + s.to_array().len() + s.chunks(2).len()\n"+
		"        + s.splitn(\" \", 2).len() + s.lines().len() + s.split(\" \").len()\n"+
		"        + http.http_path_segments(\"/a/b\").len();\n"+
		"}\n")
	out, err = exec.Command(bin, segments).CombinedOutput()
	if err != nil {
		t.Fatalf("census failed on an entry using std/string's segment producers: %v\n%s", err, out)
	}
	segmentsReport := string(out)
	t.Logf("census of an entry over std/string's segment producers:\n%s", segmentsReport)
	if strings.Contains(segmentsReport, "view element escapes its source") {
		t.Errorf("a segment producer still hands a view to an owning array; every piece has to "+
			"be materialised, as the runtime twins in asmcore.fern do.\n%s", segmentsReport)
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
