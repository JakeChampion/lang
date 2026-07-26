package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRScalarArrayAppendFieldX86_64 guards the struct-literal field
// value `S { xs: xs.append(v) }` / `S { xs: xs.with(i, v) }` where `xs` is a
// bare-ident SCALAR-array local or param (i32[]/u32[]/f64[]/i64[]/sub-word) —
// the parser.dl_collect_stmt shape `DeferAcc { flags: flags.append(d.on_error) }`
// over an `i32[]`. Before the fix only a FIELD-READ receiver (`s.flags.append(v)`,
// via scalar_arr_field_type) was admitted; a bare-ident receiver bailed the
// whole module to the AST emitter.
//
// `.append`/`.with` clone the receiver into a fresh sole-owned array, so the
// struct owns the result with NO alias-inc (the receiver is borrowed for the
// copy), exactly like the field-read and `[…]`-literal cases.
//
// Each case asserts the IR path (compact asm, well under the AST-fallback size)
// and the oracle-pinned exit code, so a routing regression (back to AST) or an
// rc/heap-accounting miscompile is caught. The programs are small (well under
// the eligible_core module-size budget) so they route through IR.
func TestSelfHostIRScalarArrayAppendFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		// Bare-ident scalar-array LOCAL receiver as a struct field value.
		{"append-local-i32-arr-field", `struct Acc { flags: i32[] }
function build(n: i32): Acc {
	var flags: i32[] = [];
	var i: i32 = 0;
	while (i < n) { flags = flags.append(i); i = i + 1; }
	return Acc { flags: flags.append(99) };
}
function main(): i32 {
	var a: Acc = build(4);
	var sum: i32 = 0; var j: i32 = 0;
	while (j < a.flags.len()) { sum = sum + a.flags[j]; j = j + 1; }
	return a.flags.len() + sum;
}`, 5 + (0 + 1 + 2 + 3 + 99)}, // len 5 + sum 105 -> 110

		// Bare-ident scalar-array PARAM receiver, via `.with`.
		{"with-param-i32-arr-field", `struct Acc { flags: i32[] }
function rebuild(flags: i32[], v: i32): Acc { return Acc { flags: flags.with(0, v) }; }
function main(): i32 {
	var base: i32[] = [10, 20, 30];
	var a: Acc = rebuild(base, 7);
	return a.flags.len() + a.flags[0] + a.flags[1] + a.flags[2];
}`, 3 + 7 + 20 + 30}, // len 3 + 7+20+30=57 -> 60

		// The faithful dl_collect_stmt shape: a union `Stm`, a result struct
		// with an enum-array field (`actions`, append of a union field-access),
		// an enum-array literal local (`stmts`), AND the bare-ident scalar-array
		// append (`flags.append(d.on_error)`) that was the frontier — all in one
		// struct literal, recursing through a list walker.
		{"dl-collect-stmt-shape", `struct SVar { x: i32 }
struct SDefer { action: Stm, on_error: i32 }
type Stm = SVar | SDefer;
struct Acc { stmts: Stm[], actions: Stm[], flags: i32[] }
function collect(st: Stm, actions: Stm[], flags: i32[]): Acc {
	match (st) {
		SDefer(d) => {
			var one: Stm[] = [d.action];
			return Acc { stmts: one, actions: actions.append(d.action), flags: flags.append(d.on_error) };
		},
		_ => { var one: Stm[] = [st]; return Acc { stmts: one, actions: actions, flags: flags }; }
	}
}
function collect_list(stmts: Stm[], actions: Stm[], flags: i32[]): Acc {
	var acc: Stm[] = actions;
	var afl: i32[] = flags;
	var i: i32 = 0;
	while (i < stmts.len()) {
		var r: Acc = collect(stmts[i], acc, afl);
		acc = r.actions;
		afl = r.flags;
		i = i + 1;
	}
	return Acc { stmts: stmts, actions: acc, flags: afl };
}
function main(): i32 {
	var prog: Stm[] = [];
	prog = prog.append(SVar { x: 1 });
	prog = prog.append(SDefer { action: SVar { x: 2 }, on_error: 1 });
	prog = prog.append(SDefer { action: SVar { x: 3 }, on_error: 0 });
	var r: Acc = collect_list(prog, [], []);
	return r.actions.len() * 10 + r.flags.len();
}`, 2*10 + 2}, // two SDefers -> actions len 2, flags len 2 -> 22
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 28000 {
				t.Fatalf("%s: asm is %d bytes — expected compact IR output, not an AST-fallback bail", tc.name, len(asm))
			}
			progBin := buildBin(t, gcc, dir, "saaf_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
