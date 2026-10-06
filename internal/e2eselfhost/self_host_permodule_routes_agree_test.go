package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostPerModuleRoutesAgree: the one-unit-per-process route
// (`-per-module-emit N [-func-range LO:HI]`) and the batched route
// (`-per-module-emit-all`) cut their units from the same typed lowering of the
// whole program, so every unit is byte-identical between them. A one-function
// window budget shards the library modules, so the windowed cut is compared as
// well, and the batched units link and run. The entry is never sharded, so the
// bodies on its tail (`first`'s instances, the drop helpers) and `_start` are
// emitted once.
func TestSelfHostPerModuleRoutesAgree(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "routesagreedriver")

	proj := t.TempDir()
	mustWrite(t, proj, "shapes.fern", `pub struct Point { x: i32, y: i32, tag: string }
pub function make(x: i32, y: i32): Point { return Point { x: x, y: y, tag: "p" + "q" }; }
@noinline pub function first[T](xs: T[], d: T): T { if (xs.len() == 0) { return d; } return xs[0]; }
pub function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
`)
	mustWrite(t, proj, "mid.fern", `import "./shapes";
pub function total(ps: shapes.Point[]): i32 {
    let t: i32 = 0;
    for p in ps { t = t + p.x + p.y + p.tag.len(); }
    return t;
}
pub function scaled(k: i32): i32 { return shapes.apply((v: i32): i32 => v * k, 3); }
pub function picked(): i32 { return shapes.first([7, 8], 0) + shapes.first(["a", "bb"], "z").len(); }
`)
	mustWrite(t, proj, "main.fern", `import "./shapes";
import "./mid";
function picks(): i32 { return mid.picked(); }
function main(): i32 { return mid.total([shapes.make(1, 2), shapes.make(3, 4)]) + mid.scaled(2) + picks(); }
`)
	entry := filepath.Join(proj, "main.fern")
	const budget = 1

	drive := func(args ...string) string {
		t.Helper()
		out, err := runX86_64Bin(runner, driverBin, append([]string{entry}, args...)...).Output()
		if err != nil {
			var stderr []byte
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = ee.Stderr
			}
			t.Fatalf("driver %v: %v\n%s", args, err, stderr)
		}
		return string(out)
	}

	shape, err := parsePMModuleShape(drive("-per-module-shape"))
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]int, len(shape))
	sizes := make([]int, len(shape))
	for i, m := range shape {
		counts[i] = m.functions
		if fi, serr := os.Stat(filepath.Join(proj, m.namespace+".fern")); serr == nil && m.namespace != "__entry" {
			sizes[i] = int(fi.Size())
		}
	}
	entryIdx := pmEntryIndex(shape)
	if entryIdx < 0 || counts[entryIdx] < 2 {
		t.Fatalf("the entry must have several functions for a one-function budget to test its exemption: %+v", shape)
	}
	jobs := planPmEmitWindows(counts, sizes, entryIdx, budget)
	sharded := false
	for _, j := range jobs {
		if j.lo > 0 {
			sharded = true
		}
	}
	if !sharded {
		t.Fatal("no module was sharded, so the windowed cut goes uncompared")
	}

	var needArgs []string
	for _, ln := range strings.Split(drive("-per-module-needs"), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}
	outDir := filepath.Join(proj, "units")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	drive("-per-module-emit-all", "-out-dir", outDir, "-func-budget", strconv.Itoa(budget))
	written, err := filepath.Glob(filepath.Join(outDir, "unit_*.s"))
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != len(jobs) {
		t.Fatalf("emit-all wrote %d units, the plan has %d", len(written), len(jobs))
	}
	var all strings.Builder
	for _, w := range written {
		b, err := os.ReadFile(w)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	defs := map[string]int{}
	for _, ln := range strings.Split(all.String(), "\n") {
		if strings.HasSuffix(ln, ":") && (ln == "_start:" || strings.HasPrefix(ln, "__fn_shapes__first__") || strings.HasPrefix(ln, "__fn___sem_")) {
			defs[ln]++
		}
	}
	if defs["_start:"] != 1 {
		t.Errorf("_start is defined %d times across the units, want 1", defs["_start:"])
	}
	instances := 0
	for d, n := range defs {
		if strings.HasPrefix(d, "__fn_shapes__first__") {
			instances++
		}
		if n != 1 {
			t.Errorf("%s is defined %d times across the units, want 1", strings.TrimSuffix(d, ":"), n)
		}
	}
	if instances == 0 {
		t.Errorf("no instance of first was emitted, so the entry's tail went unchecked")
	}

	var objs []string
	for _, j := range jobs {
		args := append([]string{"-per-module-emit", strconv.Itoa(j.modIdx)}, needArgs...)
		if !(j.lo == 0 && j.hi == j.count) {
			args = append(args, "-func-range", strconv.Itoa(j.lo)+":"+strconv.Itoa(j.hi))
		}
		single := drive(args...)
		path := filepath.Join(outDir, "unit_"+pmUnitKey(j)+".s")
		batched, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("emit-all wrote no unit %s: %v", pmUnitKey(j), err)
		}
		if single != string(batched) {
			t.Errorf("unit %s differs between the routes (%d vs %d bytes, first diff at line %d)",
				pmUnitKey(j), len(single), len(batched), firstDiffLine(single, string(batched)))
		}
		objs = append(objs, path)
	}
	sort.Strings(objs)
	bin := filepath.Join(proj, "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, lout)
	}
	cmd := runX86_64Bin(runner, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 28 {
		t.Errorf("program exited %d, want 28", code)
	}
}
