package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Calls whose result a function returns as it is, at a depth past the stack
// of wasmtime. The lowering turns no call here into a loop: `is_even` gets
// `is_odd` inline, so its call to itself returns through a join, and `ping`
// and `pong` call each other. A closure that the caller selects at run time
// returns from a call through the table.
const selfHostWasmTailCallSource = `import "std/i32";

function is_even(n: i32): boolean {
    if (n == 0) { return true; }
    return is_odd(n - 1);
}

function is_odd(n: i32): boolean {
    if (n == 0) { return false; }
    return is_even(n - 1);
}

function ping(n: i32, k: (i32) => i32): i32 {
    if (n == 0) { return k(n); }
    return pong(n - 1, k);
}

function pong(n: i32, k: (i32) => i32): i32 {
    return ping(n, k);
}

function apply(k: (i32) => i32, x: i32): i32 {
    return k(x);
}

function main(): i32 {
    let even = is_even(1000000);
    let hops = ping(700000, (m: i32): i32 => m + 1);
    let ks: ((i32) => i32)[] = [(m: i32): i32 => m + 1, (m: i32): i32 => m * 2];
    let applied = 0;
    for i in 0..4 {
        applied += apply(ks[i % 2], i);
    }
    print("even=" + (if (even) { "yes" } else { "no" }) + " hops=" + hops.to_string() + " applied=" + applied.to_string());
    return __rc_underflow_count();
}
`

const selfHostWasmTailCallWant = "0|even=yes hops=1 applied=12\n"

func TestSelfHostWasmTailCall(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostWasmTailCallSource), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"wasm32-wasi", "x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			got, report, _ := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, "")
			if got != selfHostWasmTailCallWant {
				t.Fatalf("answered %q, want %q\nreport: %s", got, selfHostWasmTailCallWant, report)
			}
		})
	}
}
