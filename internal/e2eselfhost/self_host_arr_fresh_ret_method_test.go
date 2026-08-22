package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- A METHOD returning a fresh array is reclaimed like a free function (#7259)
//
// The strict-fresh "ARR:" registry admitted only FREE functions
// (arr_fresh_ret_fns_of gated on an empty receiver_type, and the entry keyed the
// bare name). Every consumer was already method-aware — owned_fresh_call_callee
// resolves "<Base>.<method>", the same key the registry's struct entries use —
// so they were looking up a key the producer never wrote, and a method returning
// a fresh array was released by nobody in any consumption form:
//
//	rounds     100      200      400
//	live      4000     8000    16000     frees=0 throughout
//
// Exactly 2.0x per doubling — UNBOUNDED, where the byte-identical free function
// is flat at 0. That is what separates this from the other two defects on #7259,
// which are bounded per object.
//
// Three sites had open-coded the free-function half of the lookup. Two are fixed
// here (the discarded-statement reclaim and the `.len()` receiver reclaim, the
// latter now routed through owned_fresh_call_callee rather than re-deriving the
// admission); the `mk()[i]` read reclaim already used the shared resolver and
// started working the moment the registry carried the key.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

const arrFreshMethProlog = "struct H { v: i32 }\n" +
	"function (h: H) mkm(): i32[] { return [h.v, h.v + 1]; }\n"

const arrFreshMethMain = "\nfunction main(): i32 { var hh: H = H { v: 5 }; var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 100) { BODY i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

type arrFreshMethCase struct {
	name string
	src  string
	want int
}

func arrFreshMethBody(body string) string {
	return arrFreshMethProlog + strings.Replace(arrFreshMethMain, "BODY", body, 1)
}

func arrFreshMethCases() []arrFreshMethCase {
	return []arrFreshMethCase{
		{
			// The result is consumed by `.len()` and never bound — the receiver
			// reclaim. This is the row that was 4000 / 8000 / 16000.
			name: "method_len",
			src:  arrFreshMethBody("t = t + hh.mkm().len();"),
			want: 34,
		},
		{
			// The result is DISCARDED outright — the statement reclaim, which had
			// an ExprIdent-callee-only match.
			name: "method_discarded",
			src:  arrFreshMethBody("hh.mkm();"),
			want: 0,
		},
		{
			// An INDEX read — the #6491 shape. Its site already used the shared
			// resolver, so this row is pinned to catch the registry key regressing
			// rather than the site.
			name: "method_index_read",
			src:  arrFreshMethBody("t = t + hh.mkm()[0];"),
			want: 2,
		},
		{
			// The result is BOUND and read twice, so it outlives the call. The
			// binding owns it and the exit sweep frees it; none of the three
			// reclaim sites may fire. This is the case a release in the wrong
			// place turns into an over-release of a live binding, and the
			// underflow check below is what would show it.
			name: "method_result_bound",
			src:  arrFreshMethBody("var a: i32[] = hh.mkm(); t = t + a[0] + a.len();"),
			want: 36,
		},
		{
			// The returned array is DERIVED from the receiver's field rather than
			// built from its scalars — still fresh, since the elements are copied
			// scalars, so still admitted.
			name: "method_derived_from_field",
			src: "struct G { xs: i32[] }\n" +
				"function (g: G) copy(): i32[] { return [g.xs[0], g.xs[1]]; }\n" +
				"function main(): i32 { var gg: G = G { xs: [1, 2, 3] }; var t: i32 = 0; var i: i32 = 0; " +
				"while (i < 100) { t = t + gg.copy().len(); i = i + 1; } " +
				"if (__rc_underflow() != 0) { return 99; } return t % 83; }",
			want: 34,
		},
	}
}

// TestSelfHostArrFreshRetMethodX86_64 — the fresh array a method returns is
// released exactly once.
//
// `allocs == frees` at `live_bytes == 0` is the leak assertion; the exit code
// carries the over-release one, since a doubly-released block returns to the
// freelist and moves neither count.
func TestSelfHostArrFreshRetMethodX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrFreshMethCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrfreshmeth_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a reclaim site "+
					"released a buffer a live binding still owns)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — the returned buffer is allocated per round, so a "+
					"missing release scales with the loop", tc.name, summary)
			}
		})
	}
}

// TestSelfHostArrFreshRetMethodRefusedX86_64 — a method returning a BORROWED
// field array must NOT be admitted to the registry.
//
// `return h.xs` hands back the receiver's own buffer, not a fresh one. Releasing
// it at the call site would free a buffer the live `hh` still owns — the failure
// this admission rule exists to prevent, and the direction that corrupts rather
// than leaks.
//
// It asserts the ANSWER and `__rc_underflow()`, not leak counts, and deliberately:
// this shape still leaks 48 bytes, which is #7259's OTHER two defects (the
// unreleased return-transfer dup, and the struct losing its deep field-drop when
// a function returns one of its array fields). Both are bounded per object and
// neither is addressed here. Asserting `live_bytes == 0` would therefore pin a
// bug rather than the refusal this case is for.
func TestSelfHostArrFreshRetMethodRefusedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	src := "struct H { xs: i32[] }\n" +
		"function (h: H) get(): i32[] { return h.xs; }\n" +
		"function main(): i32 { var hh: H = H { xs: [1, 2, 3] }; var t: i32 = 0; var i: i32 = 0; " +
		"while (i < 100) { t = t + hh.get().len(); i = i + 1; } " +
		"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

	asm := hevCompile(t, runner, driverBin, src, nil)
	progBin := buildBin(t, gcc, dir, "arrfreshmeth_borrowed_field_return", asm)
	_, exit := hevRun(t, runner, progBin)
	if exit != 51 {
		t.Errorf("borrowed_field_return exited %d, want 51 (99 = rc underflow: the "+
			"registry admitted a method that hands back its receiver's own buffer)", exit)
	}
}

// TestSelfHostArrFreshRetMethodWasmIR — the wasm sibling. Exit codes only.
func TestSelfHostArrFreshRetMethodWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh-ret method wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrFreshMethCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "arrfreshmeth_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("fresh-ret method wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostArrFreshRetMethodIRArm64 — the arm64 sibling under qemu.
func TestSelfHostArrFreshRetMethodIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrFreshMethCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "arrfreshmeth_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
