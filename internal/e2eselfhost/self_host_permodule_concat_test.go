package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeConcatFixture generates a program that lands inside asm_modload_run's
// over-budget per-module rescue band: nMod sibling modules x nFn trivial i32
// functions (701 raw merged funcs — above the 512 gate, below the 1500 cap) with
// a live closure of nMod, one call per module, so every pruned unit is
// IR-eligible. Generated and stdlib-free, so it is deterministic and cannot
// drift onto the AST path because some stdlib helper stopped lowering.
//
// Returns the entry path and the module count.
func writeConcatFixture(t *testing.T, dir string) (string, int) {
	t.Helper()
	const nMod, nFn = 7, 100
	proj := filepath.Join(dir, "concatproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var imports, calls strings.Builder
	want := 0
	for m := 0; m < nMod; m++ {
		var lib strings.Builder
		for f := 0; f < nFn; f++ {
			fmt.Fprintf(&lib, "pub function m%d_f%d(x: i32): i32 { return x + %d; }\n", m, f, m*nFn+f)
		}
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("lib%d.fern", m)), []byte(lib.String()), 0o644); err != nil {
			t.Fatalf("write lib%d: %v", m, err)
		}
		fmt.Fprintf(&imports, "import \"./lib%d\";\n", m)
		if m > 0 {
			calls.WriteString(" + ")
		}
		fmt.Fprintf(&calls, "lib%d.m%d_f0(1)", m, m)
		want += 1 + m*nFn // m*nFn+0 added to the argument 1
	}
	entry := fmt.Sprintf("%s\nfunction main(): i32 {\n    var t: i32 = %s;\n    if (t == %d) { return 0; }\n    return 1;\n}\n",
		imports.String(), calls.String(), want)
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return entryPath, nMod
}

// assertConcatProduced pins WHICH path emitted `asm`: per-unit `.S<ns>_<idx>`
// string pools mean the per-module concat, whole-program `.S<idx>` labels mean
// the merged IR path or the AST emitter. Without this the tests below would keep
// passing silently if the over-budget rescue stopped engaging — i.e. would stop
// testing the thing they are named for.
func assertConcatProduced(t *testing.T, asm []byte) {
	t.Helper()
	if !regexp.MustCompile(`(?m)^\.S[A-Za-z_][A-Za-z0-9_]*_[0-9]+:`).Match(asm) {
		t.Fatalf("expected per-unit string pools (.S<ns>_<idx>) — the per-module concat; " +
			"got none, so the over-budget rescue did not engage")
	}
	if regexp.MustCompile(`(?m)^\.S[0-9]+:`).Match(asm) {
		t.Errorf("found whole-program string labels (.S<idx>) — this routed the merged " +
			"path, not the per-module concat")
	}
}

// buildConcatDriver builds asm_modload_run from the harness base set plus its own
// import closure. The driver is always host-native (x86-64 here): it emits for
// either target via `-target`, so only ONE build is needed for both legs below.
func buildConcatDriver(t *testing.T, gcc string) (string, string) {
	t.Helper()
	dir := writeSelfHostAsmProject(t)
	// Base set plus asm_modload_run's own imports (modloader pulls in fern_toml).
	copySelfHostDriver(t, dir, "asm_modload_run.fern")
	return dir, buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "mmr")
}

// TestSelfHostPerModuleConcatX86_64 gives asm_modload_run's over-budget
// per-module concat (emit_per_module_concat, #5676) its FIRST end-to-end
// coverage on x86-64: emit → assemble → link → run.
//
// Why this did not exist before. The one test named for the over-budget rescue,
// TestSelfHostOverBudgetPerModuleIR, documents in its own header that it does
// NOT reach the concat, and concludes "nothing in the suite has" a program that
// does. That conclusion was drawn from asm_load_run, which treeshakes its merged
// module in place BEFORE consulting the size gate, so its gate always sees the
// small live closure. asm_modload_run is different: it gates on the RAW merged
// count (`asm_ir.lift_lambdas(module_with_builtins(merged)).funcs.len()`) and
// treeshakes only afterwards, to derive the reachable-name set. So the concat IS
// reachable there for any bundle over 512 raw functions — the coverage gap was
// reachability of the FIXTURE, not of the code.
//
// The assertions are ordered so a failure says which half broke:
//   - concat produced this, not the merged/AST path (assertConcatProduced).
//   - assembles + links  =>  no duplicate or dangling cross-unit symbols. This is
//     the half that actually bites: the units emit one-per-program symbols that
//     rely on dedupe_weak_defs, and a dangling runtime-helper reference links
//     nowhere. Exactly this caught a real arm64 defect (see the arm64 sibling).
//   - runs to exit 0  =>  the cross-unit calls compute the right value. main
//     returns 0 only if the sum across all 7 modules matches, so a miscompiled
//     cross-unit call is a nonzero exit rather than a silent pass.
//
// Native only: the driver resolves sibling imports by host path from argv.
func TestSelfHostPerModuleConcatX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmr := buildConcatDriver(t, gcc)
	entryPath, nMod := writeConcatFixture(t, dir)

	// Multi-module, and past the raw 512-function gate.
	cnt, err := exec.Command(mmr, entryPath, "-per-module-count").Output()
	if err != nil {
		t.Fatalf("-per-module-count failed: %v", err)
	}
	if got := strings.TrimSpace(string(cnt)); got != fmt.Sprint(nMod+1) {
		t.Fatalf("-per-module-count = %q, want %d (entry + %d libs)", got, nMod+1, nMod)
	}

	asm, err := exec.Command(mmr, entryPath).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)

	bin := buildBin(t, gcc, dir, "concat_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("per-module concat program exited %d, want 0 (cross-unit calls miscompiled)", code)
	}
}

// TestSelfHostPerModuleConcatArm64 is the arm64 leg, and the reason the arm64
// merged-leg rescue could not simply be asserted into place.
//
// The rescue landed once (#5937) with CI fully green — every aarch64 lane
// included — and was still a regression, because nothing in the suite drove the
// path. Run against this fixture it did not link:
//
//	undefined reference to `__fn_i32__gcd'
//
// The concat's entry unit always emits __fern_i32_lcm (i32_gcd and i32_lcm are
// both in asm_ir.all_runtime_need_roots), and the arm64 per-module UNIT path
// emitted lcm's `.gcd()` call as `__fn_i32__gcd` while the gcd body was emitted
// as `__fn___fern_i32_gcd`. #5937 was reverted, and the mismatch is now fixed at
// the source: irlower lowers `.gcd()` / `.lcm()` to op_call_direct on the
// __fern_i32_* helpers for every backend, so lcm's inner call resolves to the gcd
// body emitted beside it.
//
// This test is what makes that verifiable rather than asserted: the link step is
// the assertion, since a dangling cross-unit symbol is invisible to emit-only
// checks. Keeping it means a future regression in the arm64 unit path's
// runtime-helper symbols fails here instead of silently going green.
//
// This is an x86-HOST test that cross-emits for arm64, and the reason is
// structural rather than a choice: buildSelfHostBin's emit is
// e2eharness.emitDriverAsm, which calls x86_64.Emit unconditionally, so a
// self-host DRIVER binary is always x86-64 asm. There is no arm64 driver to
// build, which is why the whole TestSelfHost*Arm64 family runs the driver on x86
// and only the EMITTED program is arm64.
//
// So the requirements are: a native x86-64 host to exec the driver, plus the
// aarch64 cross toolchain to assemble/link/run the emitted program. On a native
// arm64 runner this cannot work at all — the driver is the wrong architecture
// (`fork/exec …/mmr: exec format error`), which is exactly what an earlier
// attempt to run this on the aarch64 lane hit.
//
// CI coverage therefore depends on the x86 shards HAVING the cross toolchain;
// without it this skips, as 11 sibling *Arm64 tests silently did. The selfhost
// workflow now installs gcc-aarch64-linux-gnu + qemu-user-static on the x86_64
// shards for exactly that reason.
func TestSelfHostPerModuleConcatArm64(t *testing.T) {
	hostGcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host driver is emitted as x86-64 asm (x86_64.Emit), so it must run on a native x86-64 host")
	}
	armGcc, qemu := arm64Tooling(t) // skips when the aarch64 cross toolchain is absent
	dir, mmr := buildConcatDriver(t, hostGcc)
	entryPath, _ := writeConcatFixture(t, dir)

	asm, err := exec.Command(mmr, entryPath, "-target", "arm64").Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("arm64 over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)
	// The specific symbol the reverted #5937 dangled. Checked by name as well as
	// by the link below, so a failure names the cause instead of only the effect.
	if regexp.MustCompile(`i32__gcd`).Match(asm) {
		t.Errorf("emitted a reference to __fn_i32__gcd — the arm64 unit path's " +
			"lcm/gcd symbol mismatch is back (irlower should lower .gcd() to __fern_i32_gcd)")
	}

	// Assembling + linking with the aarch64 toolchain is the real assertion: this
	// is what a dangling cross-unit runtime symbol fails.
	bin := buildBin(t, armGcc, dir, "concat_prog_arm64", string(asm))
	rc := runArm64Bin(qemu, bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 per-module concat program exited %d, want 0 (cross-unit calls miscompiled)", code)
	}
}
