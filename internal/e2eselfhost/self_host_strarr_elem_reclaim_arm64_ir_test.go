package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostStrArrElemReclaimIRArm64 is the arm64 port of the #4355 string[]
// ELEMENT reclaim (x86 sibling: TestSelfHostStrArrElemReclaimIRX86_64). Under
// qemu the reclaim is proven by CORRECTNESS (a wrong free of a live element box
// corrupts the read-back / ticks the underflow detector → 99) plus an asm-shape
// assertion on the `bl __fn___fern_str_arr_free` CALL site — present when the
// SARR gate admits the array, absent when the element-hazard walk excludes it.
// Heavy heap-exhaustion churn is left to the x86 path (too slow under qemu).
func TestSelfHostStrArrElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// userCodeCalls: a `bl <helper>` counts as a reclaim-set admission only
	// inside a USER function (`__fn_<name>:` label that isn't a runtime
	// `__fn___fern_*` body) — the __fn___fern_strarrarr_free runtime body
	// (#4355 slice 9) itself `bl`s __fn___fern_str_arr_free per inner, so a
	// whole-asm substring match would misread the runtime as an admission.
	userCodeCalls := func(asm, helper string) bool {
		inUser := false
		for _, ln := range strings.Split(asm, "\n") {
			if strings.HasPrefix(ln, "__fn_") && strings.HasSuffix(ln, ":") {
				inUser = !strings.HasPrefix(ln, "__fn___fern_")
				continue
			}
			if inUser && strings.Contains(ln, "bl "+helper) {
				return true
			}
		}
		return false
	}

	run := func(t *testing.T, prog, name string, want int, wantCall string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		hasCall := userCodeCalls(string(asm), "__fn___fern_str_arr_free")
		if wantCall == "yes" && !hasCall {
			t.Fatalf("%s: emitted arm64 asm has no __fn___fern_str_arr_free call — the string[] was not admitted to the SARR reclaim set", name)
		}
		if wantCall == "no" && hasCall {
			t.Fatalf("%s: emitted arm64 asm calls __fn___fern_str_arr_free — the element-hazard walk failed to exclude an aliased/escaping element", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (99 = over-release)", name, code, want)
		}
	}

	// RECLAIM SHAPE + VALUE: fresh-element string[] (literal + concats, sanctioned
	// self-append) over 5000 build/drop cycles — the element walk must free each
	// element exactly once (a double free ticks the underflow detector → 99) and
	// the transient reads stay correct. lens 3+3+4 = 10.
	run(t, `function build(pre: string): i32 { var xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); var tl: i32 = 0; var j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(5000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-reclaim-arm64", 0, "yes")

	// LOOP-BODY REINIT (#4353 item 4): a string[] re-DECLARED each iteration is
	// freed at the loop REBIND (emit_strarr_reclaim_store), not at a helper exit.
	// Correctness + over-release proof under qemu (the x86 sibling carries the
	// heap-exhaustion / bounded-high-water leg). xs[1]="abx" (3) + xs[2]="abyy"
	// (4) = 7 each iteration; a UAF from an early element free would read garbage
	// (bad=1) or tick the underflow detector (99). underflow 0 + value 7 → 0.
	run(t, `function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var xs: string[] = ["lit", pre + "x", pre + "yy"]; if (xs[1].len() + xs[2].len() != 7) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-reinit-loop-arm64", 0, "yes")

	// ELEMENT ALIAS BINDING excludes: `var t = xs[0]` — xs stays on the shallow
	// buffer-only dec, so t reads valid bytes and nothing double-frees. 3+2 = 5.
	run(t, `function pick(pre: string): i32 { var xs: string[] = [pre + "x", "qq"]; var t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-elem-alias-excluded-arm64", 0, "no")
}
