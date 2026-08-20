package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- `.to_string()` on a callee LOCAL, not a param (#7193 follow-up) ---------
//
// #7193 got a helper returning `n.to_string()` into the fresh-string-return
// registry. The receiver there is a PARAM, and the registry runs on bare AST, so
// the proof it uses — tostring_recv_is_scalar_param — reads the params list and
// nothing else. A helper that computes before it formats has a LOCAL receiver
// and never entered the registry, so every caller's binding leaked the result:
//
//	fmt(n) { return n.to_string(); }              allocs=400 frees=398 live=32
//	fmt(n) { var v: i32 = n*2; return v.to_string(); }  allocs=400 frees=0 live=6400
//
// against 0 on native for both — 32 B/round for a one-word difference in the
// helper. The same `.to_string()` written INLINE at the call site was already
// flat, which is what isolates it to the registry rather than the lowering.
//
// The local's ANNOTATION carries exactly the proof the param's does, so
// decl_scalar_local reads it. Every declaration of the name must be scalar: this
// scan has no scopes, and a nested block may shadow the name with a
// pointer-shaped local — `h_shadow_nonscalar` is that case, and it must stay
// refused.

func toStringLocalSrc(helper string, rounds int) string {
	return fmt.Sprintf(`import "std/i32";
%s
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var s: string = fmt(i); acc = (acc + s.len()) %% 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(%d) + __rc_underflow_count() * 100; }
`, helper, rounds)
}

// toStringLocalFlatCases must reach the same constant residue the working
// PARAM-receiver spelling has (32 bytes, whatever the round count) rather than
// growing 32 B a round. Both `want`s of every case were adjudicated against BOTH
// oracles (`bin/fern -interp` and the native x86-64 backend).
var toStringLocalFlatCases = []struct {
	name    string
	helper  string
	want100 int
	want200 int
}{
	{
		// The shape: compute into a local, then format it.
		name: "local_recv",
		helper: `function fmt(n: i32): string {
    var v: i32 = n * 2;
    return v.to_string();
}`,
		want100: 63,
		want200: 90,
	},
	{
		// The result bound to a local on the way out, which routes through
		// str_local_is_fresh_ret and needs the same proof one level in.
		name: "local_recv_via_binding",
		helper: `function fmt(n: i32): string {
    var v: i32 = n * 2;
    var r: string = v.to_string();
    return r;
}`,
		want100: 63,
		want200: 90,
	},
	{
		// i64, not just i32 — is_scalar_type_name is the whole test.
		name: "i64_local_recv",
		helper: `function fmt(n: i32): string {
    var v: i64 = (n as i64) * 2;
    return v.to_string();
}`,
		want100: 63,
		want200: 90,
	},
	{
		// Declared inside a nested block, and a second declaration at top level.
		// Both are scalar, so both admit; the scan has to find the nested one.
		name: "nested_block_decls",
		helper: `function fmt(n: i32): string {
    if (n % 2 == 0) { var v: i32 = n * 2; return v.to_string(); }
    var w: i32 = n * 3;
    return w.to_string();
}`,
		want100: 71,
		want200: 7,
	},
}

// toStringLocalHazardCases must keep leaking. Each asserts the ANSWER: crediting
// one of these would hand a caller's binding ownership of a box it does not own,
// and the `__rc_underflow_count() * 100` term in `main` is what separates that
// from a leak.
var toStringLocalHazardCases = []struct {
	name string
	src  string
	want int
}{
	{
		// A nested block SHADOWS the name with a string local. The scan has no
		// scopes, so it cannot tell which `v` the return means — every
		// declaration must be scalar or the name refuses.
		name: "shadowed_by_nonscalar",
		src: toStringLocalSrc(`function fmt(n: i32): string {
    var v: i32 = n * 2;
    if (n > 1000000) { var v: string = "x"; return v; }
    return v.to_string();
}`, 200),
		want: 90,
	},
	{
		// UN-annotated. The type is what carries the proof here; guessing from
		// the initialiser would need the lowering state this scan does not have,
		// and refusing costs a leak where guessing could cost an over-release.
		name: "unannotated_local",
		src: toStringLocalSrc(`function fmt(n: i32): string {
    var v = n * 2;
    return v.to_string();
}`, 200),
		want: 90,
	},
	{
		// A STRUCT receiver whose `to_string` is a source-declared method, not
		// the builtin decimal-text producer. is_scalar_type_name refuses the
		// annotation, so the registry never claims it — this one is already flat
		// by another route, and the case is here to pin that this widening did
		// not divert it.
		name: "struct_recv_method",
		src: toStringLocalSrc(`struct P { a: i32 }
function (p: P) to_string(): string { return "p"; }
function fmt(n: i32): string {
    var v: P = P { a: n };
    return v.to_string();
}`, 200),
		want: 18,
	},
}

// TestSelfHostToStringLocalRecvReclaimX86_64 — the residue must be the same at
// 100 and 200 rounds, which is what says the helper's result is released rather
// than accumulating. An absolute zero is not the assertion: the working
// PARAM-receiver spelling carries the same 32-byte constant, so a ceiling would
// be a budget for the rest of the Perceus port rather than a gate on this shape.
//
// Non-vacuity: every case fails this against the parent commit, at 32 B/round.
func TestSelfHostToStringLocalRecvReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range toStringLocalFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			live := func(rounds, want int) int64 {
				t.Helper()
				src := toStringLocalSrc(tc.helper, rounds)
				asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
				name := fmt.Sprintf("tostrlocal_%s_%d", tc.name, rounds)
				stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, name, asm))
				if exit != want {
					t.Fatalf("%s exited %d, want %d (a value >= 100 is an rc over-release, not a leak)",
						name, exit, want)
				}
				summary := ""
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, "leakcheck: ") {
						summary = line
					}
				}
				if summary == "" {
					t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
				}
				var allocs, frees, l int64
				if _, err := fmtSscan(summary, &allocs, &frees, &l); err != nil {
					t.Fatalf("%s: parse %q: %v", name, summary, err)
				}
				if allocs == 0 {
					t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
				}
				t.Logf("%s: %s", name, summary)
				return l
			}
			l100, l200 := live(100, tc.want100), live(200, tc.want200)
			if l100 != l200 {
				t.Errorf("a `.to_string()` on a callee LOCAL leaks per round: live_bytes=%d at 100 "+
					"rounds and %d at 200. The same helper with a PARAM receiver is flat, so the "+
					"registry proof is what is missing, not the lowering (#7193 follow-up)", l100, l200)
			}
		})
	}
}

// TestSelfHostToStringLocalRecvHazardsX86_64 pins the refusals. A wrong answer
// or a crash means a caller's binding took ownership of a box it does not own.
// Each `want` came from the interpreter and the native backend agreeing.
func TestSelfHostToStringLocalRecvHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range toStringLocalHazardCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			bin := buildBin(t, gcc, dir, "tostrlocal_hazard_"+tc.name, asm)
			if _, exit := hevRun(t, runner, bin); exit != tc.want {
				t.Errorf("exited %d, want %d — %d+100 would be an rc over-release", exit, tc.want, tc.want)
			}
		})
	}
}

func toStringLocalAllCases() []struct {
	name string
	src  string
	want int
} {
	var out []struct {
		name string
		src  string
		want int
	}
	for _, tc := range toStringLocalFlatCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, toStringLocalSrc(tc.helper, 200), tc.want200})
	}
	for _, tc := range toStringLocalHazardCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, tc.src, tc.want})
	}
	return out
}

// TestSelfHostToStringLocalRecvArm64 checks the answer on the self-host arm64
// backend, which emits, assembles and links the binary itself.
func TestSelfHostToStringLocalRecvArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range toStringLocalAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tostrlocal_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}

// TestSelfHostToStringLocalRecvWasm is the wasm leg. Every `want` is under
// WASI's 126 ceiling, so an over-release (+100) is still expressible.
func TestSelfHostToStringLocalRecvWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host to_string-local wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range toStringLocalAllCases() {
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
			watFile := filepath.Join(dir, "tostrlocal_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}
