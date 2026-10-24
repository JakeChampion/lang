package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostEnumPayloadBorrowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (97 = payload read back wrong → use-after-free; 99 = over-release detector)", name, code, want)
		}
	}

	run(t, `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { let kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { let first: P = mkp(); let out: N[] = [first.node]; return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    let xs: N[] = build();
    let churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    let churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, "enum-field-into-array-literal", 0)

	run(t, `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { let kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { let first: P = mkp(); let out: N[] = []; out = out.append(first.node); return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    let xs: N[] = build();
    let churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    let churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, "enum-field-into-append", 0)

	run(t, `enum N { Leaf(i32), Seq(N[]) }
function total(n: N): i32 {
    match (n) {
        Leaf(v) => { return v; },
        Seq(xs) => {
            let t: i32 = 0;
            let i: i32 = 0;
            while (i < xs.len()) { t = t + total(xs[i]); i = i + 1; }
            return t;
        }
    }
}
function main(): i32 {
    let kids: N[] = [Leaf(1), Leaf(2), Leaf(4)];
    let n: N = Seq(kids);
    let k: i32 = 0;
    while (k < 8) {
        if (total(n) != 7) { return 97; }
        let churn: i32[] = [k + 100, k + 200, k + 300];
        if (churn[0] != k + 100) { return 97; }
        k = k + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, "enum-array-payload-arm-borrow", 0)

	run(t, `enum Doc { Empty, Lines(string[]) }
function width(d: Doc): i32 {
    match (d) {
        Empty => { return 0; },
        Lines(ls) => {
            let w: i32 = 0;
            let i: i32 = 0;
            while (i < ls.len()) { w = w + ls[i].len(); i = i + 1; }
            return w;
        }
    }
}
function main(): i32 {
    let d: Doc = Lines(["ab", "cde"]);
    let k: i32 = 0;
    while (k < 8) {
        if (width(d) != 5) { return 97; }
        let churn: i32[] = [k, k + 1, k + 2];
        if (churn[2] != k + 2) { return 97; }
        k = k + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, "enum-strarr-payload-arm-borrow", 0)

	run(t, `function width(d: Option[i32[]]): i32 {
    match (d) {
        Some(xs) => {
            let w: i32 = 0;
            let i: i32 = 0;
            while (i < xs.len()) { w = w + xs[i]; i = i + 1; }
            return w;
        },
        None => { return 0; }
    }
}
function main(): i32 {
    let base: i32[] = [1, 2, 4];
    let d: Option[i32[]] = Some(base);
    let k: i32 = 0;
    while (k < 8) {
        if (width(d) != 7) { return 97; }
        let churn: i32[] = [k + 9, k + 8, k + 7];
        if (churn[0] != k + 9) { return 97; }
        k = k + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, "option-array-payload-arm-borrow", 0)
}

var selfHostEnumFieldAliasCases = []struct {
	name string
	src  string
	exit int
}{
	{"alias-into-array-literal", `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { let kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { let first: P = mkp(); let tmp: N = first.node; let out: N[] = [tmp]; return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    let xs: N[] = build();
    let churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    let churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"alias-into-append", `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { let kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { let first: P = mkp(); let tmp: N = first.node; let out: N[] = []; out = out.append(tmp); return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    let xs: N[] = build();
    let churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    let churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"alias-into-two-containers", `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { let kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { let first: P = mkp(); let tmp: N = first.node; let a: N[] = [tmp]; let b: N[] = []; b = b.append(tmp); return a.append(b[0]); }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    let xs: N[] = build();
    let churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    let churn2: N = Leaf(55);
    if (xs.len() != 2) { return 96; }
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(xs[1]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostEnumFieldAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (97 = enum box read back wrong → use-after-free; 99 = over-release detector)", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostEnumFieldAliasIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
