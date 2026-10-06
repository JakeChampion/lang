package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Fold pass reassociates a constant chain across a non-constant
// operand — `x + 1 + 2` becomes `x + 3`, `(m & 12) & 10` becomes
// `m & 8`, etc. — for the associative ops (add / mul / and / or / xor).
// The two constants are separated by the first op, so the plain
// two-adjacent-constant fold can't reach them; only reassociation does.
// The folded constants must compute the same result on every backend.
//
// f(10)=13, h(15)=8, o(0)=5, m(2)=30, chain(0)=6 → 13+8+5+30+6 = 62.
const constReassocFoldSrc = `function f(x: i32): i32 { return x + 1 + 2; }
function h(x: i32): i32 { return (x & 12) & 10; }
function o(x: i32): i32 { return x | 1 | 4; }
function m(x: i32): i32 { return x * 3 * 5; }
function chain(x: i32): i32 { return x + 1 + 2 + 3; }
function main(): i32 {
    return f(10) + h(15) + o(0) + m(2) + chain(0);
}
`

func TestInterpConstReassocFold(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(constReassocFoldSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 62 {
		t.Errorf("exit = %d, want 62\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ConstReassocFold(t *testing.T) {
	out, code := compileAndRunX86_64(t, constReassocFoldSrc)
	if code != 62 {
		t.Errorf("exit = %d, want 62\n%s", code, out)
	}
}

func TestArm64ConstReassocFold(t *testing.T) {
	out, code := compileAndRunArm64(t, constReassocFoldSrc)
	if code != 62 {
		t.Errorf("exit = %d, want 62\n%s", code, out)
	}
}

func TestWASMConstReassocFold(t *testing.T) {
	if code := runWasm(t, constReassocFoldSrc); code != 62 {
		t.Errorf("wasm exit = %d, want 62", code)
	}
}

// i64 chains reassociate too, keeping the wide width: big(5)=35,
// mask(255)=15 → 50.
const constReassocFoldI64Src = `function big(x: i64): i64 { return x + 10i64 + 20i64; }
function mask(x: i64): i64 { return (x & 255i64) & 15i64; }
function main(): i32 {
    return (big(5i64) + mask(255i64)) as i32;
}
`

func TestX86_64ConstReassocFoldI64(t *testing.T) {
	out, code := compileAndRunX86_64(t, constReassocFoldI64Src)
	if code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestWASMConstReassocFoldI64(t *testing.T) {
	if code := runWasm(t, constReassocFoldI64Src); code != 50 {
		t.Errorf("wasm exit = %d, want 50", code)
	}
}
