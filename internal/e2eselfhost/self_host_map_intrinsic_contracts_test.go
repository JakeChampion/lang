package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// `core/map` is written against the runtime directly — `__fern_rc_inc`,
// `__fern_str_dec`, `__map_hash_seed`, `__fern_drop_arr_ptr`, `__fern_arr_dec`
// are called by name rather than inserted by the RC lowering. None is a
// declared Fern function, so none had a contract, so `semsource` refused every
// declaration that called one and every declaration that called those.
//
// That was 91% of every refusal in a 512-program fernsmith census: 7,168 on
// `__fern_rc_inc`, 4,096 on `__fern_str_dec`, 512 on `__map_hash_seed`, against
// 396 for `unresolved result type`. 23 declarations per program, one module,
// multiplied by every program that imports it — which is all of them.
//
// This gate is the shape rather than the count: any program that touches a Map
// must leave no `no semantic contract` refusal naming a `__fern_*` or `__map_*`
// helper. A sixth helper arriving in core/map without a contract fails here.
func TestSelfHostMapIntrinsicContracts(t *testing.T) {
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

	entry := filepath.Join(dir, "entry.fern")
	// Enough Map surface to drag in the string-keyed and pointer-valued
	// columns: those are where the retain / drop helpers live.
	writeEntry(t, entry, `import "core/map";
function main(): i32 {
    var m: Map[string, string] = Map { "a": "x" };
    m = m.insert("b", "y");
    var n: Map[i32, i32[]] = Map { 1: [2, 3] };
    n = n.insert(4, [5]);
    return m.len() + n.len();
}
`)
	out, err := exec.Command(census, entry).CombinedOutput()
	if err != nil {
		t.Fatalf("census failed: %v\n%s", err, out)
	}
	report := string(out)
	t.Logf("census of a Map-using entry:\n%s", report)

	helper := regexp.MustCompile(`no semantic contract: (__fern_\w+|__map_\w+)`)
	if m := helper.FindAllStringSubmatch(report, -1); len(m) > 0 {
		var names []string
		for _, one := range m {
			names = append(names, one[1])
		}
		t.Errorf("the producer has no contract for %s — core/map calls the runtime by name, "+
			"so each helper it uses needs a row in semsource.rc_contracts.\n%s",
			strings.Join(names, ", "), report)
	}

	count := func(label string) int {
		m := regexp.MustCompile(`(?m)^` + label + `\s+(\d+)`).FindStringSubmatch(report)
		if m == nil {
			t.Fatalf("no %q line in the report:\n%s", label, report)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	// The stdlib closure of a Map program is thousands of declarations; a
	// handful refusing for unrelated reasons is fine, a collapse is not.
	measured, produced := count("measured"), count("produced")
	if measured == 0 {
		t.Fatalf("census measured nothing:\n%s", report)
	}
	if produced*100 < measured*99 {
		t.Errorf("produced %d of %d (%.1f%%), want at least 99%%\n%s",
			produced, measured, 100*float64(produced)/float64(measured), report)
	}
}
