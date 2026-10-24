package e2e

import (
	"testing"
)

const closureArgChurnSrc = `import "std/i32";
import "std/string";

function each(f: (i32[]) => i32, g: (string) => i32, n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        t = t + f([1, 2, i]) + g("key-that-is-past-sso-" + i.to_string());
        i = i + 1;
    }
    return t;
}

function churn(n: i32): i32 {
    let h: (i32[]) => i32 = (xs: i32[]) => xs.len() + xs[2];
    let s: (string) => i32 = (k: string) => k.len();
    let t: i32 = each(h, s, n);
    if (t <= 0) { return 99; }
    if ((__heap_bump_bytes() as i32) < 65536) { return 0; }
    return 1;
}

function main(): i32 { return churn(20000); }`

func TestX86_64ClosureCallArgRecycles(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, closureArgChurnSrc); code != 0 {
		t.Errorf("closure-call argument churn: got exit %d, want 0 (heap bump < 64 KiB — a fresh argument to a closure call must be released after the call)", code)
	}
}

func TestArm64ClosureCallArgRecycles(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, closureArgChurnSrc); code != 0 {
		t.Errorf("closure-call argument churn: got exit %d, want 0", code)
	}
}

func TestWASMClosureCallArgRecycles(t *testing.T) {
	if got := runWasm(t, closureArgChurnSrc); got != 0 {
		t.Errorf("closure-call argument churn: got exit %d, want 0", got)
	}
}
