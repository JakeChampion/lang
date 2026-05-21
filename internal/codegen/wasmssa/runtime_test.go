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

// TestRuntimeDualReturn — `(c, a, b) → if (c) return a; else
// return b`. Tests the dual-return-diamond emission both arms.
func TestRuntimeDualReturn(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		c := f.AddParam()
		a := f.AddParam()
		b := f.AddParam()
		entry := f.NewBlock()
		thenB := f.NewBlock()
		elseB := f.NewBlock()
		f.SetBrIf(entry, c, thenB, elseB)
		f.SetRet(thenB, a)
		f.SetRet(elseB, b)
		return f
	}
	got := runWasmtime(t, build(), "1", "11", "22")
	if got != 11 {
		t.Errorf("c=1 ? 11 : 22 → %d, want 11", got)
	}
	got = runWasmtime(t, build(), "0", "11", "22")
	if got != 22 {
		t.Errorf("c=0 ? 11 : 22 → %d, want 22", got)
	}
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

// TestRuntimeFactorial — self-recursive factorial through
// the SSA → wasm path. Verifies that OpCall lowers correctly
// to `call 0` (the func referring to itself) and that the
// recursion bottoms out at n <= 1.
func TestRuntimeFactorial(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("factorial")
		n := f.AddParam()
		entry := f.NewBlock()
		thenB := f.NewBlock()
		elseB := f.NewBlock()
		merge := f.NewBlock()
		one := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[0].Imm = 1
		cond := f.AddOp(entry, ssa.OpLe, n, one)
		f.SetBrIf(entry, cond, thenB, elseB)
		tOne := f.AddOp(thenB, ssa.OpConstInt)
		thenB.Ops[0].Imm = 1
		f.SetBr(thenB, merge)
		eOne := f.AddOp(elseB, ssa.OpConstInt)
		elseB.Ops[0].Imm = 1
		subOne := f.AddOp(elseB, ssa.OpSub, n, eOne)
		recur := f.AddOp(elseB, ssa.OpCall, subOne)
		elseB.Ops[2].Str = "factorial"
		prod := f.AddOp(elseB, ssa.OpMul, n, recur)
		f.SetBr(elseB, merge)
		phi := f.AddPhi(merge, tOne, prod)
		f.SetRet(merge, phi)
		return f
	}
	cases := []struct {
		n, want int
	}{
		{0, 1}, {1, 1}, {2, 2}, {3, 6}, {4, 24}, {5, 120}, {6, 720}, {10, 3628800},
	}
	for _, c := range cases {
		got := runWasmtimeNamed(t, build(), "factorial", strconv.Itoa(c.n))
		if got != c.want {
			t.Errorf("factorial(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// runWasmtimeNamed is like runWasmtime but uses `funcName` as
// the wasmtime --invoke target (not just "main"). The export
// also uses `funcName`.
func runWasmtimeNamed(t *testing.T, f *ssa.Func, funcName string, args ...string) int {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping runtime e2e")
	}
	mod, err := EmitModule(f, funcName)
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(p, mod, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	cmdArgs := append([]string{"run", "--invoke", funcName, p}, args...)
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

// TestRuntimeExtend8S — sign-extend the low byte. For
// x=0x80 (-128 as signed byte) the result must be -128 as
// i32 (0xffffff80, i.e. -128). For x=0x7f (127) it stays
// 127.
func TestRuntimeExtend8S(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		x := f.AddParam()
		entry := f.NewBlock()
		r := f.AddOp(entry, ssa.OpExtend8S, x)
		f.SetRet(entry, r)
		return f
	}
	cases := []struct {
		in, want int32
	}{
		{0x7f, 127},
		{0x80, -128},
		{0xff, -1},
		{0, 0},
		{0x100, 0},    // high bits dropped, low byte 0
		{0x180, -128}, // high bits dropped, low byte 0x80
	}
	for _, c := range cases {
		got := runWasmtime(t, build(), strconv.Itoa(int(c.in)))
		if int32(got) != c.want {
			t.Errorf("extend8s(0x%x) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestRuntimeExtend16S — sign-extend the low halfword.
func TestRuntimeExtend16S(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		x := f.AddParam()
		entry := f.NewBlock()
		r := f.AddOp(entry, ssa.OpExtend16S, x)
		f.SetRet(entry, r)
		return f
	}
	cases := []struct {
		in, want int32
	}{
		{0x7fff, 32767},
		{0x8000, -32768},
		{0xffff, -1},
		{0, 0},
		{0x10000, 0},
	}
	for _, c := range cases {
		got := runWasmtime(t, build(), strconv.Itoa(int(c.in)))
		if int32(got) != c.want {
			t.Errorf("extend16s(0x%x) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestRuntimeI64Add — hand-built i64 (a, b) → a + b. Validates
// that the function type signature uses i64, the locals are
// declared i64, and the i64.add opcode is emitted.
func TestRuntimeI64Add(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		b := f.AddParam()
		f.ParamWidths = []int8{64, 64}
		f.ReturnWidth = 64
		entry := f.NewBlock()
		sumOp := &ssa.Op{
			Kind:   ssa.OpAdd,
			Result: f.NewValue(),
			Args:   []ssa.Value{a, b},
			Width:  64,
		}
		entry.Ops = append(entry.Ops, sumOp)
		f.SetRet(entry, sumOp.Result)
		return f
	}
	cases := []struct {
		a, b, want int64
	}{
		{1, 2, 3},
		{1 << 40, 1 << 40, 1 << 41},
		{-1, 1, 0},
		{1<<62 - 1, 1, 1 << 62},
	}
	for _, c := range cases {
		got := runWasmtime(t, build(), strconv.FormatInt(c.a, 10), strconv.FormatInt(c.b, 10))
		if int64(got) != c.want {
			t.Errorf("i64 add(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestRuntimeF64Add — hand-built f64 (a, b) → a + b. Verifies
// the function signature is (f64,f64)→f64, f64.add is emitted,
// and the f64 round-trips through wasmtime.
func TestRuntimeF64Add(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		a := f.AddParam()
		b := f.AddParam()
		f.ParamWidths = []int8{64, 64}
		f.ParamFloats = []bool{true, true}
		f.ReturnWidth = 64
		f.ReturnFloat = true
		entry := f.NewBlock()
		sumOp := &ssa.Op{Kind: ssa.OpFAdd, Result: f.NewValue(), Args: []ssa.Value{a, b}}
		entry.Ops = append(entry.Ops, sumOp)
		f.SetRet(entry, sumOp.Result)
		return f
	}
	mod, err := EmitModule(build(), "main")
	if err != nil {
		t.Fatalf("EmitModule: %v", err)
	}
	validateModule(t, mod)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(p, mod, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p, "1.5", "2.25")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:\n%s", err, se.String())
	}
	out := strings.TrimSpace(so.String())
	if out != "3.75" {
		t.Errorf("f64 add(1.5, 2.25) wasmtime stdout = %q, want 3.75", out)
	}
}

// TestRuntimeStringFirstByte — OpConstString with "Hello",
// load the first byte (i32.load8_u), return as i32. The data
// segment + memory must wire up so that byte 'H' (= 72) is
// actually in linear memory at the pool offset.
func TestRuntimeStringFirstByte(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		entry := f.NewBlock()
		strV := f.AddOp(entry, ssa.OpConstString)
		entry.Ops[0].Str = "Hello"
		b0 := f.AddOp(entry, ssa.OpLoad8U, strV)
		f.SetRet(entry, b0)
		return f
	}
	got := runWasmtime(t, build())
	if got != 72 { // 'H'
		t.Errorf("first byte = %d, want 72 ('H')", got)
	}
}

// TestRuntimeStringByteIndex — string + index → return the
// indexed byte. Verifies pool offsets + memory addressing for
// non-zero indices.
func TestRuntimeStringByteIndex(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		i := f.AddParam()
		entry := f.NewBlock()
		strV := f.AddOp(entry, ssa.OpConstString)
		entry.Ops[0].Str = "abcdef"
		addrOp := &ssa.Op{Kind: ssa.OpAdd, Result: f.NewValue(), Args: []ssa.Value{strV, i}}
		entry.Ops = append(entry.Ops, addrOp)
		byteOp := f.AddOp(entry, ssa.OpLoad8U, addrOp.Result)
		f.SetRet(entry, byteOp)
		return f
	}
	for idx, want := range []int{'a', 'b', 'c', 'd', 'e', 'f'} {
		got := runWasmtime(t, build(), strconv.Itoa(idx))
		if got != want {
			t.Errorf("byte at %d = %d, want %d (%c)", idx, got, want, want)
		}
	}
}

// TestRuntimeI64Mix — a function with i64 params, a small loop,
// and an i64 return. Verifies i64 locals + i64 ops + i64 phi
// (via the relooper) all flow correctly.
func TestRuntimeI64Mix(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		n := f.AddParam()
		f.ParamWidths = []int8{64}
		f.ReturnWidth = 64

		entry := f.NewBlock()
		header := f.NewBlock()
		body := f.NewBlock()
		done := f.NewBlock()

		// total = 0 (i64)
		zero := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[0].Imm = 0
		entry.Ops[0].Width = 64
		f.SetBr(entry, header)

		// phi: i = phi(zero, i+1); total = phi(zero, total+i)
		iVal := f.NewValue()
		iPhi := &ssa.Op{Kind: ssa.OpPhi, Result: iVal, Args: []ssa.Value{zero, ssa.Value{}}}
		header.Ops = append(header.Ops, iPhi)
		totVal := f.NewValue()
		totPhi := &ssa.Op{Kind: ssa.OpPhi, Result: totVal, Args: []ssa.Value{zero, ssa.Value{}}}
		header.Ops = append(header.Ops, totPhi)
		condOp := &ssa.Op{Kind: ssa.OpLt, Result: f.NewValue(), Args: []ssa.Value{iVal, n}, Width: 64}
		header.Ops = append(header.Ops, condOp)
		f.SetBrIf(header, condOp.Result, body, done)

		one := f.AddOp(body, ssa.OpConstInt)
		body.Ops[0].Imm = 1
		body.Ops[0].Width = 64
		newTotOp := &ssa.Op{Kind: ssa.OpAdd, Result: f.NewValue(), Args: []ssa.Value{totVal, iVal}, Width: 64}
		body.Ops = append(body.Ops, newTotOp)
		newIOp := &ssa.Op{Kind: ssa.OpAdd, Result: f.NewValue(), Args: []ssa.Value{iVal, one}, Width: 64}
		body.Ops = append(body.Ops, newIOp)
		iPhi.Args[1] = newIOp.Result
		totPhi.Args[1] = newTotOp.Result
		f.SetBr(body, header)

		f.SetRet(done, totVal)
		return f
	}
	// sum(0..n-1) computed in i64.
	cases := []struct {
		n, want int64
	}{
		{0, 0},
		{5, 10},
		{100, 4950},
		{10_000, 10_000 * 9_999 / 2},
	}
	for _, c := range cases {
		got := runWasmtime(t, build(), strconv.FormatInt(c.n, 10))
		if int64(got) != c.want {
			t.Errorf("i64 sum(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// TestRuntimeStringDedup — two OpConstString ops with the same
// literal should share a single offset in the pool. Returns the
// difference of the two pointers — 0 means dedupped.
func TestRuntimeStringDedup(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		entry := f.NewBlock()
		a := f.AddOp(entry, ssa.OpConstString)
		entry.Ops[0].Str = "shared"
		b := f.AddOp(entry, ssa.OpConstString)
		entry.Ops[1].Str = "shared"
		diffOp := &ssa.Op{Kind: ssa.OpSub, Result: f.NewValue(), Args: []ssa.Value{a, b}}
		entry.Ops = append(entry.Ops, diffOp)
		f.SetRet(entry, diffOp.Result)
		return f
	}
	got := runWasmtime(t, build())
	if got != 0 {
		t.Errorf("dedup diff = %d, want 0", got)
	}
}

// TestRuntimeMemoryRoundtrip — store the param to memory at a
// fixed offset, read it back, return the loaded value. Verifies
// memory section + i32 load/store actually function under
// wasmtime, not just validate.
func TestRuntimeMemoryRoundtrip(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		v := f.AddParam()
		entry := f.NewBlock()
		addr := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[0].Imm = 256
		storeOp := &ssa.Op{Kind: ssa.OpStore, Args: []ssa.Value{addr, v}}
		entry.Ops = append(entry.Ops, storeOp)
		loaded := f.AddOp(entry, ssa.OpLoad, addr)
		f.SetRet(entry, loaded)
		return f
	}
	for _, v := range []int32{0, 1, -1, 42, 1 << 16, -1 << 16} {
		got := runWasmtime(t, build(), strconv.Itoa(int(v)))
		if int32(got) != v {
			t.Errorf("roundtrip(%d) = %d, want %d", v, got, v)
		}
	}
}

// TestRuntimeAllocAndStore — alloc 8 bytes, write the param to
// the returned pointer, read it back. Verifies the bump
// allocator returns a usable pointer and that writes/reads at
// that pointer produce the right value.
func TestRuntimeAllocAndStore(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("main")
		v := f.AddParam()
		entry := f.NewBlock()
		size := f.AddOp(entry, ssa.OpConstInt)
		entry.Ops[0].Imm = 8
		ptr := f.AddOp(entry, ssa.OpAlloc, size)
		storeOp := &ssa.Op{Kind: ssa.OpStore, Args: []ssa.Value{ptr, v}}
		entry.Ops = append(entry.Ops, storeOp)
		loaded := f.AddOp(entry, ssa.OpLoad, ptr)
		f.SetRet(entry, loaded)
		return f
	}
	for _, v := range []int32{0, 7, -99, 12345} {
		got := runWasmtime(t, build(), strconv.Itoa(int(v)))
		if int32(got) != v {
			t.Errorf("alloc+store(%d) = %d, want %d", v, got, v)
		}
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
