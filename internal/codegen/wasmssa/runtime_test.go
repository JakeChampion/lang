package wasmssa

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// runWasmtime builds a wasm module via EmitModule, writes it
// to a temp file, invokes `wasmtime run --invoke main` on it,
// and returns the i32 the function produced on stdout. SKIPs
// the test if wasmtime isn't on PATH.
//
// Invoke args are passed as positional strings; wasmtime
// parses them as i32 per the func signature.
func runWasmtime(t *testing.T, f *ssa.Func, args ...string) int {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping runtime e2e")
	}
	mod, err := EmitModule(f, "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(p, mod, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	cmdArgs := append([]string{"run", "--invoke", "main", p}, args...)
	cmd := exec.Command(wasmtime, cmdArgs...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:\n%s", err, se.String())
	}
	out := strings.TrimSpace(so.String())
	v, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("parse wasmtime stdout %q: %v", out, err)
	}
	return v
}

// TestRuntimeConstReturn — single-block, no args, returns 42.
func TestRuntimeConstReturn(t *testing.T) {
	f := ssa.NewFunc("main")
	entry := f.NewBlock()
	c := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 42
	f.SetRet(entry, c)

	got := runWasmtime(t, f)
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// TestRuntimeAdd — `(a, b) → a + b`, invoked with 17 and 25
// yields 42.
func TestRuntimeAdd(t *testing.T) {
	f := ssa.NewFunc("main")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, ssa.OpAdd, a, b)
	f.SetRet(entry, sum)

	got := runWasmtime(t, f, "17", "25")
	if got != 42 {
		t.Errorf("17 + 25 → %d, want 42", got)
	}
}

// TestRuntimeIfElseTrue — `(c, a, b) → c ? a : b`. Pick c=1
// → returns a (10).
func TestRuntimeIfElseTrue(t *testing.T) {
	f := buildIfElseSelect()
	got := runWasmtime(t, f, "1", "10", "20")
	if got != 10 {
		t.Errorf("c=1 ? 10 : 20 → %d, want 10", got)
	}
}

// TestRuntimeIfElseFalse — same shape, c=0 → returns b (20).
func TestRuntimeIfElseFalse(t *testing.T) {
	f := buildIfElseSelect()
	got := runWasmtime(t, f, "0", "10", "20")
	if got != 20 {
		t.Errorf("c=0 ? 10 : 20 → %d, want 20", got)
	}
}

// buildIfElseSelect builds the if-else select function used
// by the two runtime tests above.
func buildIfElseSelect() *ssa.Func {
	f := ssa.NewFunc("main")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, a, b)
	f.SetRet(merge, phi)
	return f
}

// TestRuntimeWhileLoopCounter — `(n) → counter loop`:
//
//	i := 0
//	while i < n { i++ }
//	return i
//
// Invoking with n=7 yields 7.
func TestRuntimeWhileLoopCounter(t *testing.T) {
	f := ssa.NewFunc("main")
	n := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	zero := f.AddOp(entry, ssa.OpConstInt)
	entry.Ops[0].Imm = 0
	f.SetBr(entry, header)

	phiRes := f.NewValue()
	phiOp := &ssa.Op{Kind: ssa.OpPhi, Result: phiRes, Args: []ssa.Value{zero, ssa.Value{}}}
	header.Ops = append(header.Ops, phiOp)
	cond := f.AddOp(header, ssa.OpLt, phiRes, n)
	f.SetBrIf(header, cond, body, done)

	one := f.AddOp(body, ssa.OpConstInt)
	body.Ops[0].Imm = 1
	inc := f.AddOp(body, ssa.OpAdd, phiRes, one)
	phiOp.Args[1] = inc
	f.SetBr(body, header)

	f.SetRet(done, phiRes)

	got := runWasmtime(t, f, "7")
	if got != 7 {
		t.Errorf("counter loop n=7 → %d, want 7", got)
	}
}

// TestRuntimeArithSweep — checks every supported binary op
// produces the value Go's int32 op produces. Keeps the test
// matrix small (one pair of operands).
func TestRuntimeArithSweep(t *testing.T) {
	cases := []struct {
		name string
		kind ssa.OpKind
		a, b int32
		want int32
	}{
		{"add", ssa.OpAdd, 10, 3, 13},
		{"sub", ssa.OpSub, 10, 3, 7},
		{"mul", ssa.OpMul, 10, 3, 30},
		{"div_s", ssa.OpDiv, 10, 3, 3},
		{"div_u", ssa.OpDivU, 10, 3, 3},
		{"rem_s", ssa.OpRem, 10, 3, 1},
		{"rem_u", ssa.OpRemU, 10, 3, 1},
		{"and", ssa.OpAnd, 0xff, 0x0f, 0x0f},
		{"or", ssa.OpOr, 0xf0, 0x0f, 0xff},
		{"xor", ssa.OpXor, 0xff, 0x0f, 0xf0},
		{"shl", ssa.OpShl, 1, 3, 8},
		{"shr_s", ssa.OpShr, 16, 2, 4},
		{"shr_u", ssa.OpShrU, 16, 2, 4},
		{"eq", ssa.OpEq, 5, 5, 1},
		{"ne", ssa.OpNe, 5, 5, 0},
		{"lt_s", ssa.OpLt, 3, 5, 1},
		{"le_s", ssa.OpLe, 5, 5, 1},
		{"gt_s", ssa.OpGt, 5, 3, 1},
		{"ge_s", ssa.OpGe, 5, 5, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := ssa.NewFunc("main")
			a := f.AddParam()
			b := f.AddParam()
			entry := f.NewBlock()
			r := f.AddOp(entry, c.kind, a, b)
			f.SetRet(entry, r)
			got := runWasmtime(t, f, strconv.Itoa(int(c.a)), strconv.Itoa(int(c.b)))
			if int32(got) != c.want {
				t.Errorf("%s(%d, %d) → %d, want %d", c.name, c.a, c.b, got, c.want)
			}
		})
	}
}
