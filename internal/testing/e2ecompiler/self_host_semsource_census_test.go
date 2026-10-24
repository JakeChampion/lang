package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostSemanticSourceCensusLoadsTheStdlib(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("census driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/semsource_census_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	bin := filepath.Join(dir, "census")
	build := exec.Command(buildFernCLIBin(t), "-target", "x86-64-linux",
		"-embed", stdlib, "-o", bin, filepath.Join(dir, "drivers/semsource_census_run.fern"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the census failed: %v\n%s", err, out)
	}

	entry := filepath.Join(dir, "entry.fern")
	writeEntry(t, entry, `import "std/test";
function t_one(): test.TestOutcome { return test.assert_eq(1, 1); }
function main(): i32 {
    let r: test.TestRunner = test.test_new("census");
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
	if n := countIn(shakenReport, "bodyless"); n != 0 {
		t.Errorf("bodyless = %d, want 0: the prepass drops an uninstantiated template, so nothing "+
			"here should be measured without a body.\n%s", n, shakenReport)
	}
	if got, want := countIn(shakenReport, "measured"), countIn(shakenReport, "produced"); got != want {
		t.Errorf("measured %d and produced %d: the shaken entry has nothing the boundary "+
			"refuses.\n%s", got, want, shakenReport)
	}
	prepass := filepath.Join(dir, "prepass.fern")
	writeEntry(t, prepass, `function main(): i32 {
    function rec(n: i32): i32 {
        if (n <= 0) { return 0; }
        return rec(n - 1);
    }
    return rec(3);
}
`)
	out, err = exec.Command(bin, prepass).CombinedOutput()
	if err != nil {
		t.Fatalf("census failed: %v\n%s", err, out)
	}
	prepassReport := string(out)
	t.Logf("census of a self-recursive nested function:\n%s", prepassReport)
	pm, pp := countIn(prepassReport, "measured"), countIn(prepassReport, "produced")
	if pp != pm {
		t.Errorf("produced %d of %d: the census is measuring a module the production "+
			"prepass would have rewritten. The CLI lowers "+
			"lift_lambdas_typed(parser.module_with_builtins_typed(merged)); this driver has to "+
			"run the same prepass or it reports the instrument's refusals as the "+
			"boundary's.\n%s", pp, pm, prepassReport)
	}

	if strings.Contains(shakenReport, "uninstantiated generic") {
		t.Errorf("an uninstantiated template still appears in the refusal histogram; it is not a "+
			"refusal, it is a declaration with nothing to measure.\n%s", shakenReport)
	}

	injected := filepath.Join(dir, "injected.fern")
	writeEntry(t, injected, `function code(e: IoError): i32 {
    match (e) {
        NotFound(p) => { return 1; },
        PermissionDenied(p) => { return 2; },
        AlreadyExists(p) => { return 3; },
        InvalidUtf8(p) => { return 4; },
        Interrupted => { return 5; },
        Unsupported => { return 6; },
        Other(p, m, _) => { return 7; }
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

	segments := filepath.Join(dir, "segments.fern")
	writeEntry(t, segments, "import \"std/string\";\nimport \"std/http\";\n"+
		"function main(): i32 {\n"+
		"    let s: string = \"a b\" + \" c\";\n"+
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

	orphan := filepath.Join(dir, "orphan.fern")
	writeEntry(t, orphan, `import "std/definitely_not_a_module";
function main(): i32 { return 0; }
`)
	out, err = exec.Command(bin, orphan).CombinedOutput()
	if err == nil {
		t.Fatalf("census exited 0 on an unresolvable import; it must refuse to report a "+
			"partial program:\n%s", out)
	}
	want := "semsource_census_run: " + orphan + `:1:1: error[E086]: unknown stdlib module "std/definitely_not_a_module"`
	if !strings.Contains(string(out), want) {
		t.Errorf("census did not name the unresolved import and its file:\n%s\nwant %s", out, want)
	}
}

func writeEntry(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
