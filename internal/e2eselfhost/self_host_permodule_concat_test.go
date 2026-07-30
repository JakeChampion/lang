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

// TestSelfHostPerModuleConcatX86_64 gives asm_modload_run's over-budget
// per-module concat (emit_per_module_concat, #5676) its FIRST end-to-end
// coverage: emit → assemble → link → run.
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
// Hence the generated fixture: 7 sibling modules x 100 trivial i32 functions =
// 701 raw merged functions, inside the rescue's 512..1500 band, with a live
// closure of 7 (one call per module) so every pruned unit is IR-eligible. Being
// generated and stdlib-free, it is deterministic and cannot drift onto the AST
// path because some stdlib helper stopped lowering.
//
// The assertions are ordered so a failure says which half broke:
//   - per-unit `.S<ns>_<idx>` string pools present and whole-program `.S<idx>`
//     absent  =>  the concat produced this, not the merged IR or AST path. Without
//     this the test would silently keep passing if the rescue stopped engaging.
//   - assembles + links  =>  no duplicate or dangling cross-unit symbols. This is
//     the half that actually bites: the units emit one-per-program symbols that
//     rely on dedupe_weak_defs, and a dangling runtime-helper reference links
//     nowhere. Exactly this caught a real arm64 defect (see below).
//   - runs to exit 0  =>  the cross-unit calls compute the right value. main
//     returns 0 only if the sum across all 7 modules matches, so a miscompiled
//     cross-unit call is a nonzero exit rather than a silent pass.
//
// arm64 is deliberately NOT covered yet. Giving the arm64 merged leg the same
// rescue (the #3457 slice-5 prerequisite) is blocked on a defect this fixture
// found: on the per-module UNIT path the arm64 runtime emits __fern_i32_lcm whose
// body calls `__fn_i32__gcd`, while the gcd body is emitted as
// `__fn___fern_i32_gcd` — an unlinkable dangling reference
// (`undefined reference to __fn_i32__gcd`). i32_gcd/i32_lcm are both in
// asm_ir.all_runtime_need_roots(), so the concat's entry unit always emits lcm.
// The single-module arm64 IR path resolves the same helper correctly, so the
// mismatch is specific to the unit path. Add the arm64 leg here once that is
// fixed; until then arm64 over-budget bundles keep using the AST emitter, which
// is why asm_arm64.fern's AST emitter cannot be deleted yet.
//
// Native only: the driver resolves sibling imports by host path from argv.
func TestSelfHostPerModuleConcatX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	// The harness base set plus asm_modload_run's own import closure
	// (modloader pulls in fern_toml).
	for _, name := range []string{"flatten.fern", "modloader.fern", "fern_toml.fern", "asm_modload_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmr := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "mmr")

	// The fixture lives in its own directory so the driver's sibling-import
	// resolution sees only these modules.
	proj := filepath.Join(dir, "concatproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const nMod, nFn = 7, 100 // 701 raw merged funcs: > 512, < 1500
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
	// WHICH path produced it: per-unit pools mean the concat; whole-program
	// `.S<idx>` labels would mean the merged IR path (or the AST emitter), i.e.
	// the rescue silently stopped engaging and this test stopped testing it.
	if !regexp.MustCompile(`(?m)^\.S[A-Za-z_][A-Za-z0-9_]*_[0-9]+:`).Match(asm) {
		t.Fatalf("expected per-unit string pools (.S<ns>_<idx>) — the per-module concat; " +
			"got none, so the over-budget rescue did not engage")
	}
	if regexp.MustCompile(`(?m)^\.S[0-9]+:`).Match(asm) {
		t.Errorf("found whole-program string labels (.S<idx>) — this routed the merged " +
			"path, not the per-module concat")
	}

	// Assembles, links (no duplicate/dangling cross-unit symbols), and computes
	// the right cross-unit sum.
	bin := buildBin(t, gcc, dir, "concat_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("per-module concat program exited %d, want 0 (cross-unit calls miscompiled)", code)
	}
}
