package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSelfHostPerModuleEmitAllX86_64 validates the single-process
// `-per-module-emit-all` mode (#3457 slice 2): it emits every module's unit(s)
// in ONE process, deriving the whole-program side-table bases ONCE
// (compute_wp_bases) and sharing them across all units — as INDIVIDUAL string[]
// params (emit_module_ir_unit_flat), never a string[][] across a boundary (the
// slice-2 RC blocker) — instead of the per-process path re-deriving them per unit
// (~20 s/unit even for a 3-function module, the dominant serial-emit cost).
//
// The proof is byte-identity: emit-all's units must be identical to the
// per-process emit's units for the SAME window plan. That establishes (a) sharing
// the once-derived bases changes no output and (b) emit-all's in-driver windowing
// matches the harness's. Then the emit-all units link into a working compiler, and
// the wall-times of both paths are logged so the speedup is visible.
//
// It runs the full per-process baseline AND the batched emit-all (~19 min), so it
// is env-gated (RUN_EMITALL_CHECK=1) rather than run in every CI lane — the same
// treatment as the per-module fixpoint proof. The fast CI guard for the
// per-module path stays TestSelfHostModloadPerModuleWholeCompilerX86_64.
func TestSelfHostPerModuleEmitAllX86_64(t *testing.T) {
	// CI-DARK: RUN_EMITALL_CHECK — ~19 min, because it runs the full
	// per-process baseline AND the batched emit-all to compare them. The
	// property it proves (emit-all's units are byte-identical to the
	// per-process ones) is a one-off design proof, not a per-PR regression
	// risk; the standing CI guard for the per-module path is
	// TestSelfHostModloadPerModuleWholeCompilerX86_64.
	if os.Getenv("RUN_EMITALL_CHECK") == "" {
		t.Skip("set RUN_EMITALL_CHECK=1 to run the heavy emit-all byte-identity + speedup proof (~19 min; #3457 slice 2)")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "emitall_driver")
	entry := filepath.Join(dir, "asm_modload_run.fern")

	driveT := func(t *testing.T, args ...string) (string, error) {
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
		return string(out), err
	}
	drive := func(args ...string) (string, error) { return driveT(t, args...) }

	countOut, err := drive("-per-module-count")
	if err != nil {
		t.Fatalf("-per-module-count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("-per-module-count = %q (n=%d), want >= 10", countOut, n)
	}

	needsOut, err := drive("-per-module-needs")
	if err != nil {
		t.Fatalf("-per-module-needs: %v", err)
	}
	var needArgs []string
	for _, ln := range strings.Split(needsOut, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	fcOut, err := drive("-per-module-func-counts")
	if err != nil {
		t.Fatalf("-per-module-func-counts: %v", err)
	}
	var funcCounts []int
	for _, ln := range strings.Split(strings.TrimSpace(fcOut), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			c, cerr := strconv.Atoi(s)
			if cerr != nil {
				t.Fatalf("-per-module-func-counts non-int %q: %v", s, cerr)
			}
			funcCounts = append(funcCounts, c)
		}
	}
	const shardThreshold = 100
	modBytes := perModuleSourceBytes(t, driveT, dir, n)
	jobs := planPmEmitWindows(funcCounts, modBytes, shardThreshold)

	// Key helper: the driver names a window by its shard_ns — the base ns for the
	// FIRST window (lo==0), whether or not that window is the whole module (the
	// "lo=0 shard keeps the base ns" invariant), and base + "_s<lo>" for lo>0.
	// So the key is the module index for lo==0 and "<modIdx>_s<lo>" otherwise —
	// matching both the per-process unit content and emit-all's file naming
	// (unit_<modIdx>[_s<lo>].s).
	key := func(modIdx, lo, hi, count int) string {
		if lo == 0 {
			return strconv.Itoa(modIdx)
		}
		return strconv.Itoa(modIdx) + "_s" + strconv.Itoa(lo)
	}

	// 1. Per-process baseline: emit each planned window in its own process.
	perProc := map[string]string{}
	ppStart := time.Now()
	for _, j := range jobs {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(j.modIdx)}, needArgs...)
		if !(j.lo == 0 && j.hi == j.count) {
			emitArgs = append(emitArgs, "-func-range", strconv.Itoa(j.lo)+":"+strconv.Itoa(j.hi))
		}
		unit, derr := drive(emitArgs...)
		if derr != nil || len(unit) == 0 {
			t.Fatalf("per-process emit mod %d [%d:%d]: %v (%d bytes)", j.modIdx, j.lo, j.hi, derr, len(unit))
		}
		perProc[key(j.modIdx, j.lo, j.hi, j.count)] = unit
	}
	ppDur := time.Since(ppStart)
	t.Logf("per-process: %d units in %.1fs (%d processes, each re-deriving the side-tables)", len(perProc), ppDur.Seconds(), len(jobs))

	// 2. emit-all: batched across processes, each sharing the side-table
	// derivation across its batch of units. Emitting ALL units in one process
	// OOMs (~16 GB) because the per-window emit's net working set accumulates;
	// batching bounds it while still deriving the ~22 side-tables once per batch
	// (not once per unit). The flat unit order in the driver matches `jobs`
	// (module-major, window-minor, identical windowing), so unit-range [b,hi)
	// emits exactly jobs[b:hi].
	outDir := filepath.Join(dir, "emitall_out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir outDir: %v", err)
	}
	const batchUnits = 8
	totalUnits := len(jobs)
	eaStart := time.Now()
	batches := 0
	for b := 0; b < totalUnits; b += batchUnits {
		hi := b + batchUnits
		if hi > totalUnits {
			hi = totalUnits
		}
		if _, derr := drive("-per-module-emit-all", "-out-dir", outDir,
			"-func-budget", strconv.Itoa(shardThreshold),
			"-unit-range", strconv.Itoa(b)+":"+strconv.Itoa(hi)); derr != nil {
			t.Fatalf("emit-all batch [%d:%d]: %v", b, hi, derr)
		}
		batches++
	}
	eaDur := time.Since(eaStart)
	t.Logf("emit-all ran in %d batches of <=%d units", batches, batchUnits)

	emitAll := map[string]string{}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read outDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name() // unit_<modIdx>[_s<lo>].s
		if !strings.HasPrefix(name, "unit_") || !strings.HasSuffix(name, ".s") {
			continue
		}
		k := strings.TrimSuffix(strings.TrimPrefix(name, "unit_"), ".s")
		b, rerr := os.ReadFile(filepath.Join(outDir, name))
		if rerr != nil {
			t.Fatalf("read unit %s: %v", name, rerr)
		}
		emitAll[k] = string(b)
	}
	t.Logf("emit-all: %d units in %.1fs (ONE process, side-tables derived once) — %.1fx faster",
		len(emitAll), eaDur.Seconds(), ppDur.Seconds()/eaDur.Seconds())

	// 3. Byte-identity: emit-all's unit set must equal the per-process set.
	if len(emitAll) != len(perProc) {
		t.Fatalf("unit count differs: per-process %d, emit-all %d", len(perProc), len(emitAll))
	}
	diverged := 0
	for k, pp := range perProc {
		ea, ok := emitAll[k]
		if !ok {
			t.Errorf("emit-all missing unit %q", k)
			diverged++
			continue
		}
		if ea != pp {
			diverged++
			if diverged <= 3 {
				t.Errorf("unit %q diverged: per-process %d bytes, emit-all %d bytes (first diff line %d)",
					k, len(pp), len(ea), firstDiffLine(pp, ea))
			}
		}
	}
	if diverged > 0 {
		t.Fatalf("emit-all NOT byte-identical to per-process: %d/%d units differ", diverged, len(perProc))
	}
	t.Logf("byte-identity holds: emit-all == per-process across all %d units", len(perProc))

	// 4. The emit-all units link into a working compiler and run. Link in
	// deterministic `jobs` order (module-major, window-minor) — the same order
	// the per-module whole-compiler test links, since a Go map's iteration order
	// is randomised and object order affects the linked image.
	var objs []string
	entryUnits := 0
	for _, j := range jobs {
		k := key(j.modIdx, j.lo, j.hi, j.count)
		if strings.Contains(emitAll[k], "\n_start:\n") || strings.HasPrefix(emitAll[k], "_start:\n") {
			entryUnits++
		}
		objs = append(objs, filepath.Join(outDir, "unit_"+k+".s"))
	}
	if entryUnits != 1 {
		t.Fatalf("expected exactly one entry unit (_start), got %d", entryUnits)
	}
	compilerBin := filepath.Join(dir, "emitall_compiler")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", compilerBin)...)
	if lout, lerr := exec.Command(gcc, linkArgs...).CombinedOutput(); lerr != nil {
		t.Fatalf("link emit-all compiler: %v\n%s", lerr, lout)
	}
	// Smoke-run: the emit-all-built compiler compiles a trivial program.
	prog := filepath.Join(dir, "smoke.fern")
	if werr := os.WriteFile(prog, []byte("function main(): i32 { return 7; }\n"), 0o644); werr != nil {
		t.Fatalf("write smoke prog: %v", werr)
	}
	var scmd *exec.Cmd
	if len(runner) == 0 {
		scmd = exec.Command(compilerBin, prog)
	} else {
		scmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), prog)...)
	}
	sout, serr := scmd.Output()
	if serr != nil || len(sout) == 0 {
		t.Fatalf("emit-all compiler smoke run: %v (%d bytes asm)", serr, len(sout))
	}
	if !strings.Contains(string(sout), "call __fn_main") {
		t.Fatalf("emit-all compiler emitted no `call __fn_main` (%d bytes)", len(sout))
	}
	t.Logf("emit-all compiler links and compiles a program (%d bytes asm)", len(sout))
}
