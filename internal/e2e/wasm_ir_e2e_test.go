// E2E tests for the IR-driven WASM emitter (wasm.EmitFromIR).
// They run the same way as wasm_e2e_test.go — under wasmtime when
// it's installed, skipped otherwise — and confirm the IR emitter's
// output is functionally equivalent to the AST emitter's on a
// representative corpus.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasm"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// invokeIRWasmtime parses + checks src, lowers to IR, emits via the
// IR-driven path, runs `wasmtime run --invoke main` on the result,
// and returns stdout / stderr separately.
func invokeIRWasmtime(t *testing.T, src string) (stdout, stderr string) {
	t.Helper()
	wt := wasmtimePath(t)

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	ip, err := ir.Lower(prog, info)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	wat, err := wasm.EmitFromIR(prog, info, ip)
	if err != nil {
		t.Fatalf("EmitFromIR: %v", err)
	}

	dir := t.TempDir()
	watPath := filepath.Join(dir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}

	cmd := exec.Command(wt, "run", "--invoke", "main", watPath)
	var soBuf, seBuf bytes.Buffer
	cmd.Stdout = &soBuf
	cmd.Stderr = &seBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstdout:\n%s\nstderr:\n%s\n--- wat ---\n%s",
			err, soBuf.String(), seBuf.String(), wat)
	}
	return soBuf.String(), seBuf.String()
}

// runIRWasm is the IR-emitter equivalent of runWasm: returns the i32
// result wasmtime printed to stdout.
func runIRWasm(t *testing.T, src string) int {
	t.Helper()
	stdout, _ := invokeIRWasmtime(t, src)
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.LastIndex(ln, " "); i >= 0 {
			ln = ln[i+1:]
		}
		if n, err := strconv.Atoi(ln); err == nil {
			return n
		}
	}
	t.Fatalf("could not parse wasmtime output:\n%s", stdout)
	return 0
}

func TestIRWASME2EReturn42(t *testing.T) {
	if got := runIRWasm(t, `function main(): number { return 42; }`); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestIRWASME2EFactorial(t *testing.T) {
	src := `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}
	function main(): number { return fact(5); }`
	if got := runIRWasm(t, src); got != 120 {
		t.Errorf("got %d, want 120", got)
	}
}

func TestIRWASME2EForLoopWithBreakContinue(t *testing.T) {
	src := `function main(): number {
		var sum = 0;
		for (var i = 0; i < 10; i = i + 1) {
			if (i < 5) { continue; }
			if (i == 8) { break; }
			sum = sum + i;
		}
		return sum;
	}`
	if got := runIRWasm(t, src); got != 18 {
		t.Errorf("got %d, want 18", got)
	}
}

func TestIRWASME2ESwitch(t *testing.T) {
	src := `function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 99;
		}
		return -1;
	}
	function main(): number { return f(2) + f(3) + f(4); }`
	// 10 + 30 + 99 = 139
	if got := runIRWasm(t, src); got != 139 {
		t.Errorf("got %d, want 139", got)
	}
}

func TestIRWASME2EArraySum(t *testing.T) {
	src := `function main(): number {
		var a: number[] = [1, 2, 3, 4, 5];
		var sum = 0;
		for (var i = 0; i < 5; i = i + 1) {
			sum = sum + a[i];
		}
		return sum;
	}`
	if got := runIRWasm(t, src); got != 15 {
		t.Errorf("got %d, want 15", got)
	}
}

func TestIRWASME2EArrayMutate(t *testing.T) {
	src := `function main(): number {
		var a: number[] = [10, 20, 30];
		a[1] = 99;
		return a[0] + a[1] + a[2];
	}`
	if got := runIRWasm(t, src); got != 139 {
		t.Errorf("got %d, want 139", got)
	}
}

func TestIRWASME2EStructAndField(t *testing.T) {
	src := `struct P { x: number, y: number }
	function main(): number {
		var p: P = P { x: 10, y: 32 };
		p.y = 32;
		return p.x + p.y;
	}`
	if got := runIRWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestIRWASME2EClosure(t *testing.T) {
	src := `function makeCounter(start: number): () => number {
		function next(): number {
			return start + 1;
		}
		return next;
	}
	function main(): number {
		var f: () => number = makeCounter(41);
		return f();
	}`
	if got := runIRWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestIRWASME2EHigherOrderApply(t *testing.T) {
	src := `function add(a: number, b: number): number { return a + b; }
	function apply(f: (number, number) => number, a: number, b: number): number {
		return f(a, b);
	}
	function main(): number { return apply(add, 19, 23); }`
	if got := runIRWasm(t, src); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestIRWASME2EStringConcat(t *testing.T) {
	src := `function main(): number {
		var s: string = "hello, " + "world";
		return len(s);
	}`
	if got := runIRWasm(t, src); got != 12 {
		t.Errorf("got %d, want 12", got)
	}
}
