package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The introspection readouts — `__heap_bump_bytes()`,
// `__arr_push_shared_count()` and `__arr_push_shared_bytes()` — are the ONLY
// builtins whose self-host lowering reads a runtime `.bss` word INLINE
// (kind_tag 80, 216 and 217) rather than calling a global accessor that reads
// the word from inside its own unit, the way `__rc_underflow_count()` does
// through `__fn___fern_rc_underflow_count`.
//
// The runtime — and so every one of those words — is emitted into the ENTRY unit
// alone. An inline `movq __fern_heap_ptr(%rip)` / `adrp x0, __fern_heap_ptr`
// therefore cannot resolve from a library unit unless the definition is `.globl`.
// Whole-program builds never notice: one unit, one file, local resolution. Under
// `-per-module-emit` the link fails outright with "undefined reference to
// __fern_heap_ptr".
//
// #6058 found this the hard way on `__fern_arr_push_shared` — CI caught it only
// once the compiler's own sources called the counter, and the builtin had been
// unusable from a non-entry unit since #6023 with nothing to notice. It fixed
// that one symbol and left the heap cursor/end pair carrying the identical
// latent hazard, documented at both backend sites. This is that pair's guard, and
// it covers the cliff counter and its byte-weight sibling alongside it so the
// whole class is pinned by one test rather than one symbol by accident — every
// new inline-.bss-reading builtin belongs here at the same time as its `.globl`.
//
// The shape is the minimal trigger: the counters are called from a LIBRARY
// module, never the entry, so the reference and the definition land in different
// translation units. Exit 42 proves the read also executed and returned
// something sane (a positive high-water mark after an array literal, and a zero
// cliff count — and so a zero cliff WEIGHT — for a program that never appends to
// a shared buffer).
const perModuleCounterLibSrc = `pub function probe(): i32 {
    var xs: i32[] = [1, 2, 3];
    var mark: i64 = __heap_bump_bytes();
    if (mark <= (0 as i64)) { return 1; }
    if (xs.len() != 3) { return 2; }
    if (__arr_push_shared_bytes() != (0 as i64)) { return 3; }
    return 42 + __arr_push_shared_count();
}
`

const perModuleCounterMainSrc = `import "./counters";
function main(): i32 { return counters.probe(); }
`

// writePerModuleCounterProject stages the entry + library pair in a fresh dir and
// returns the entry path.
func writePerModuleCounterProject(t *testing.T) string {
	t.Helper()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "counters.fern"), []byte(perModuleCounterLibSrc), 0o644); err != nil {
		t.Fatalf("write counters.fern: %v", err)
	}
	entry := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(entry, []byte(perModuleCounterMainSrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return entry
}

// perModuleUnits drives the count / needs / emit sequence and writes each unit to
// disk, returning the paths. The native runtime's global string buffer holds only
// ONE emit per process, so — like a real build system — each module is emitted by
// its own driver invocation.
func perModuleUnits(t *testing.T, driverBin, entry string, targetArgs []string, minModules int) []string {
	t.Helper()
	drive := func(args ...string) string {
		t.Helper()
		full := append(append([]string{entry}, targetArgs...), args...)
		out, err := exec.Command(driverBin, full...).Output()
		if err != nil {
			t.Fatalf("driver %v: %v", args, err)
		}
		return string(out)
	}

	countOut := drive("-per-module-count")
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < minModules {
		t.Fatalf("-per-module-count = %q (n=%d err=%v), want >= %d", countOut, n, err, minModules)
	}

	var needArgs []string
	for _, ln := range strings.Split(drive("-per-module-needs"), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	dir := filepath.Dir(entry)
	var objs []string
	// Every word the probe reads INLINE must actually be referenced from a
	// non-entry unit — otherwise the link below passes for the wrong reason
	// (nothing to resolve). One entry per symbol, so dropping any one builtin's
	// inline read fails here rather than silently narrowing the test.
	sawLibRef := map[string]bool{
		"__fern_heap_ptr":        false,
		"__fern_arr_push_shared": false,
		"__fern_arr_push_copied": false,
	}
	for i := 0; i < n; i++ {
		unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
		if len(unit) == 0 {
			t.Fatalf("module %d emitted 0 bytes", i)
		}
		if !strings.Contains(unit, "_start:") {
			for sym := range sawLibRef {
				if strings.Contains(unit, sym) {
					sawLibRef[sym] = true
				}
			}
		}
		p := filepath.Join(dir, "cu"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		objs = append(objs, p)
	}
	for _, sym := range []string{"__fern_heap_ptr", "__fern_arr_push_shared", "__fern_arr_push_copied"} {
		if !sawLibRef[sym] {
			t.Fatalf("no library unit referenced %s — the test no longer drives the cross-unit shape for it", sym)
		}
	}
	return objs
}

// TestSelfHostPerModuleCounterBSSLinkX86_64 links the cross-unit counter shape on
// x86-64. Without `.globl __fern_heap_ptr` / `.globl __fern_heap_end` in
// asm_ir.fern's runtime, the link fails with "undefined reference".
func TestSelfHostPerModuleCounterBSSLinkX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "counterbss")

	entry := writePerModuleCounterProject(t)
	objs := perModuleUnits(t, driverBin, entry, nil, 2)

	bin := filepath.Join(filepath.Dir(entry), "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("per-module link failed — a counter's .bss word is not .globl (#6058 class):\n%v\n%s", err, lout)
	}

	var rcmd *exec.Cmd
	if len(runner) == 0 {
		rcmd = exec.Command(bin)
	} else {
		rcmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = rcmd.Run()
	if code := rcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("cross-unit counter program exited %d, want 42", code)
	}
}

// TestSelfHostPerModuleCounterBSSLinkArm64 is the arm64 half — the backends are
// kept in lockstep on this, and the arm64 lowering reads the same two words via
// adrp/add/ldr.
func TestSelfHostPerModuleCounterBSSLinkArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "counterbssarm64")

	entry := writePerModuleCounterProject(t)
	objs := perModuleUnits(t, driverBin, entry, []string{"-target", "arm64-linux"}, 2)

	bin := filepath.Join(filepath.Dir(entry), "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("per-module arm64 link failed — a counter's .bss word is not .globl (#6058 class):\n%v\n%s", err, lout)
	}
	rcmd := exec.Command(qemu, bin)
	_ = rcmd.Run()
	if code := rcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("cross-unit counter program exited %d, want 42 (arm64)", code)
	}
}
