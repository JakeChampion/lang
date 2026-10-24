package e2e

import (
	"testing"
)

const arrWithStrElemChurnSrc = `import "std/i32";
import "std/string";
function mks(): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i < 8) { out = out.append("kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let a: string[] = mks();
        a = a.with(3, a[5]);
        t = t + a.len() + a[3].len();
        i = i + 1;
    }
    if (t != n * 29) { return 99; }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn(20000); }`

func TestX86_64ArrayWithStrElemRecycles(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrWithStrElemChurnSrc); code != 0 {
		t.Errorf("string-element .with churn: got exit %d, want 0 (heap bump < 1 MiB — the receiver and the replaced element must recycle)", code)
	}
}

func TestArm64ArrayWithStrElemRecycles(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrWithStrElemChurnSrc); code != 0 {
		t.Errorf("string-element .with churn: got exit %d, want 0", code)
	}
}

func TestWASMArrayWithStrElemRecycles(t *testing.T) {
	if got := runWasm(t, arrWithStrElemChurnSrc); got != 0 {
		t.Errorf("string-element .with churn: got exit %d, want 0", got)
	}
}
