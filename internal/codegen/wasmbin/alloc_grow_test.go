package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// wasmFrame matches one line of wasmtime's wasm backtrace, innermost first.
var wasmFrame = regexp.MustCompile(`<wasm function (\d+)>`)

// trapUnderWasmtime runs `export` and requires the run to trap. `extraArgs`
// go ahead of the module path, so a caller can cap linear memory and make
// growth fail on demand. Returns wasmtime's stderr for frame assertions.
func trapUnderWasmtime(t *testing.T, bin []byte, export string, extraArgs ...string) string {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	args := append([]string{"run"}, extraArgs...)
	args = append(args, "--invoke", export, p)
	cmd := exec.Command("wasmtime", args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err == nil {
		t.Fatalf("wasmtime run --invoke %s succeeded (stdout %q), want a trap",
			export, strings.TrimSpace(so.String()))
	}
	out := se.String()
	if !strings.Contains(out, "wasm trap: wasm `unreachable` instruction executed") {
		t.Fatalf("did not trap on `unreachable`:\n%s", out)
	}
	return out
}

// TestAllocGrowResultIsChecked — __fern_alloc must branch on what
// memory.grow returned. Dropping it turns heap exhaustion into an
// out-of-bounds trap at whichever store first ran past the end of linear
// memory, with the allocator absent from the backtrace entirely (#6160).
func TestAllocGrowResultIsChecked(t *testing.T) {
	body := buildAllocBody(nil)

	if bytes.Contains(body, inst.InstDrop(memInstMemoryGrow(nil))) {
		t.Errorf("__fern_alloc still drops memory.grow's result; a failed grow " +
			"then reads as success and the trap lands at an unrelated store")
	}

	var check []byte
	check = memInstMemoryGrow(check)
	check = inst.InstI32Const(check, -1)
	check = numeric.InstI32Eq(check)
	check = inst.InstIfStart(check, inst.BlocktypeEmpty)
	check = inst.InstUnreachable(check)
	check = inst.InstEnd(check)
	if !bytes.Contains(body, check) {
		t.Errorf("__fern_alloc does not trap on memory.grow returning -1")
	}
}

// TestAllocBumpWraparoundIsGuarded — the bump arithmetic is i32, so a
// request that carries `end` past the 4 GiB ceiling wraps: `need` comes out
// negative, no growth is attempted, and the cursor is stored wrapped, which
// hands the caller a pointer into the static data below the heap.
func TestAllocBumpWraparoundIsGuarded(t *testing.T) {
	body := buildAllocBody(nil)

	var wrap []byte
	wrap = inst.InstLocalGet(wrap, 2) // $end
	wrap = inst.InstLocalGet(wrap, 1) // $ptr
	wrap = numeric.InstI32LtU(wrap)
	wrap = inst.InstLocalGet(wrap, 2) // $end
	wrap = inst.InstI32Const(wrap, pageRoundCeil)
	wrap = numeric.InstI32GtU(wrap)
	wrap = numeric.InstI32Or(wrap)
	wrap = inst.InstIfStart(wrap, inst.BlocktypeEmpty)
	wrap = inst.InstUnreachable(wrap)
	wrap = inst.InstEnd(wrap)
	if !bytes.Contains(body, wrap) {
		t.Errorf("__fern_alloc does not guard the bump against i32 wraparound")
	}

	var ceil []byte
	ceil = inst.InstLocalGet(ceil, 0) // $size
	ceil = inst.InstI32Const(ceil, maxAllocRequest)
	ceil = numeric.InstI32GtU(ceil)
	ceil = inst.InstIfStart(ceil, inst.BlocktypeEmpty)
	ceil = inst.InstUnreachable(ceil)
	ceil = inst.InstEnd(ceil)
	if !bytes.Contains(body, ceil) {
		t.Errorf("__fern_alloc does not reject a request too large to round")
	}
}

// allocAfterCursor builds `main` as: poke `cursor` into the bump cursor
// slot, allocate `size` bytes, return the pointer.
func allocAfterCursor(cursor, size int32) *ir.Program {
	return &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: allocCursorAddr},
			{Kind: ir.OpConstI32, I32: cursor},
			{Kind: ir.OpStore},
			{Kind: ir.OpConstI32, I32: size},
			{Kind: ir.OpAlloc},
		},
	}}}
}

// TestAllocTrapsOnCursorWraparound drives the wraparound branches for real:
// a cursor 16 bytes below 2^32 makes `ptr + size` wrap to 0, one just under
// the page-round ceiling makes `end + 65535` wrap, and a request bigger than
// maxAllocRequest wraps in the size rounding itself. All three used to
// return a pointer into low memory and store a wrapped cursor.
func TestAllocTrapsOnCursorWraparound(t *testing.T) {
	for _, c := range []struct {
		name         string
		cursor, size int32
	}{
		{"add wraps", -16, 16},                // 0xFFFFFFF0 + 16 == 0
		{"page round wraps", -4096, 16},       // 0xFFFFF000 > pageRoundCeil
		{"request too large to round", 0, -1}, // 0xFFFFFFFF > maxAllocRequest
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, err := Emit(allocAfterCursor(c.cursor, c.size))
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			trapUnderWasmtime(t, bin, "main")
		})
	}
}

// TestAllocTrapsInsideAllocOnHeapExhaustion caps linear memory below what
// the program asks for. The point of the fix is attribution, so the
// assertion is on the FRAME: the trap has to be raised in a callee of the
// entry point (i.e. inside __fern_alloc), not at whichever store first ran
// off the end of memory.
func TestAllocTrapsInsideAllocOnHeapExhaustion(t *testing.T) {
	// 32 MiB against the 8 MiB cap below.
	bin, err := Emit(allocAfterCursor(0, 32*1024*1024))
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out := trapUnderWasmtime(t, bin, "main",
		"-O", "pooling-allocator=y",
		"-O", "pooling-max-memory-size=8388608",
		"-O", "pooling-total-memories=1")
	frames := wasmFrame.FindAllStringSubmatch(out, -1)
	if len(frames) < 2 {
		t.Fatalf("want the allocator plus its caller in the backtrace, got %d "+
			"frame(s):\n%s", len(frames), out)
	}
	if frames[0][1] == frames[len(frames)-1][1] {
		t.Fatalf("innermost frame is the entry point itself, so heap exhaustion "+
			"is still unattributable to the allocator:\n%s", out)
	}
}
