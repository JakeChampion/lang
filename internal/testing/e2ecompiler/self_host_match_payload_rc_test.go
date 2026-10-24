package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

const rcMatchPayloadWorks = `function eat(n: i32): i32 {
    let s: string = "x";
    let i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function build(): string[] {
    let r: string[] = [];
    r = r.append("alpha");
    r = r.append("bravo");
    r = r.append("charlie");
    return r;
}
function main(): i32 {
    let names: string[] = build();
    let total: i32 = 0;
    let j: i32 = 0;
    while (j < names.len()) {
        let junk: i32 = eat(200);
        total = total + names[j].len();
        j = j + 1;
    }
    return total;
}`

const rcMatchPayloadUAF = `function eat(n: i32): i32 {
    let s: string = "x";
    let i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function build(): Result[string[], i32] {
    let r: string[] = [];
    r = r.append("alpha");
    r = r.append("bravo");
    r = r.append("charlie");
    return Ok(r);
}
function main(): i32 {
    match (build()) {
        Ok(names) => {
            let total: i32 = 0;
            let j: i32 = 0;
            while (j < names.len()) {
                let junk: i32 = eat(200);
                total = total + names[j].len();
                j = j + 1;
            }
            return total;
        },
        Err(_) => { return 99; }
    }
}`

const rcOptStructPayloadUAF = `struct P { xs: i32[], k: i32 }

function some_of(i: i32): Option[P] {
    let p: P = P { xs: [i, i + 1], k: i };
    let o: Option[P] = Some(p);
    return o;
}

function ok_of(i: i32): Result[P, i32] {
    let p: P = P { xs: [i, i + 1], k: i };
    let o: Result[P, i32] = Ok(p);
    return o;
}

function clobber(i: i32): i32 {
    let q: P = P { xs: [9999, 9999], k: 9999 };
    return q.k + q.xs[0];
}

function round(i: i32): i32 {
    let a: Option[P] = some_of(i);
    let b: Result[P, i32] = ok_of(i);
    let junk: i32 = clobber(i);
    let m: i32 = 0;
    let n: i32 = 0;
    match (a) { Some(p2) => { m = p2.k; }, None => { m = 0 - 1; } }
    match (b) { Ok(p3) => { n = p3.k; }, Err(e) => { n = 0 - 1; } }
    if (m != i) { return 1; }
    if (n != i) { return 1; }
    return 0;
}

function main(): i32 {
    let bad: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { bad = bad + round(r); r = r + 1; }
    return bad;
}`

const rcQualifiedVariantPayloadUAF = `enum E { A(i32[]), B }

function qual_of(i: i32): E {
    let items: i32[] = [i, i + 1, i + 2];
    let e: E = E.A(items);
    return e;
}

function bare_of(i: i32): E {
    let items: i32[] = [i, i + 1, i + 2];
    let e: E = A(items);
    return e;
}

function clobber(i: i32): i32 {
    let junk: i32[] = [7777, 7777, 7777];
    return junk[0];
}

function round(i: i32): i32 {
    let q: E = qual_of(i);
    let b: E = bare_of(i);
    let j: i32 = clobber(i);
    let m: i32 = 0;
    let n: i32 = 0;
    match (q) { A(xs) => { m = xs[0]; }, B => { m = 0 - 1; } }
    match (b) { A(ys) => { n = ys[0]; }, B => { n = 0 - 1; } }
    if (m != i) { return 1; }
    if (n != i) { return 1; }
    return 0;
}

function main(): i32 {
    let bad: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { bad = bad + round(r); r = r + 1; }
    return bad;
}`

func compileAndRunSelfHostIR(t *testing.T, gcc string, runner []string, dir, driverBin, name, src string) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed for %s: %v", name, err)
	}
	progBin := buildBin(t, gcc, dir, name, string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	return run.ProcessState.ExitCode()
}

func TestSelfHostMatchPayloadRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	t.Run("var_binding_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_work", rcMatchPayloadWorks); code != 17 {
			t.Errorf("var-binding array across an allocating call exited %d, want 17", code)
		}
	})

	t.Run("match_payload_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_uaf", rcMatchPayloadUAF); code != 17 {
			t.Errorf("match-extracted array across an allocating call exited %d, want 17", code)
		}
	})

	t.Run("struct_payload_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_optstruct_uaf", rcOptStructPayloadUAF); code != 0 {
			t.Errorf("returned option carrying a struct local exited %d, want 0 "+
				"(each nonzero round read a clobbered payload)", code)
		}
	})

	t.Run("qualified_variant_payload_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_qualvariant_uaf", rcQualifiedVariantPayloadUAF); code != 0 {
			t.Errorf("returned variant carrying an array local exited %d, want 0 "+
				"(each nonzero round read a clobbered payload)", code)
		}
	})
}
