package e2e

import (
	"testing"
)

const closureFieldChurnSrc = `import "std/i32";
import "std/i64";

struct P { name: string, f: (i32) => i32 }

function mkP(n: i32): P {
    return P { name: "provider" + n.to_string(), f: (x: i32) => x + n };
}

function round(): i32 {
    let t: i32 = 0;
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 8) { ps = ps.append(mkP(i)); i = i + 1; }
    let j: i32 = 0;
    while (j < ps.len()) { t = t + (ps[j].f)(1); j = j + 1; }
    return t;
}

function churn(n: i32): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < n) { t = t + round(); r = r + 1; }
    if (t != n * 36) { return 99; }
    if ((__heap_bump_bytes() as i32) < 65536) { return 0; }
    return 1;
}

function main(): i32 { return churn(2000); }`

func TestX86_64ClosureInStructFieldRecycles(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, closureFieldChurnSrc); code != 0 {
		t.Errorf("closure-in-struct-field churn: got exit %d, want 0 (heap bump < 64 KiB — the pair, its env and its captures must recycle with the struct)", code)
	}
}

func TestArm64ClosureInStructFieldRecycles(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, closureFieldChurnSrc); code != 0 {
		t.Errorf("closure-in-struct-field churn: got exit %d, want 0", code)
	}
}

func TestWASMClosureInStructFieldRecycles(t *testing.T) {
	if got := runWasm(t, closureFieldChurnSrc); got != 0 {
		t.Errorf("closure-in-struct-field churn: got exit %d, want 0", got)
	}
}
