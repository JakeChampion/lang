package wasmbin

import (
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
	"github.com/jakechampion/lang/internal/wasm/simd"
)

// wrapBody puts an instruction sequence in the shape a code-section entry
// has: size prefix, empty locals vector, instructions, `end`.
func wrapBody(body []byte) []byte {
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

func TestCodeUsesMemory(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want bool
	}{
		{"empty", nil, false},
		{"arithmetic only", numeric.InstI32Add(nil), false},
		// 0x28 is i32.load's opcode. As the *immediate* of an
		// i32.const it must not read as one — a raw byte search
		// would call this module memory-touching and emit a
		// section nothing uses.
		{"const with a load-shaped immediate",
			inst.InstI32Const(nil, 0x28), false},
		{"const with a store-shaped immediate",
			inst.InstI64Const(nil, 0x36), false},
		// f32/f64 constants are fixed-width immediates rather than
		// lebs, so a walker that guessed would desync inside them.
		{"f64 const of every load opcode byte",
			inst.InstF64Const(nil, 0x3e3d3c3b3a393837), false},
		{"i32.load", memory.InstI32Load(nil, 2, 0), true},
		{"i32.store", memory.InstI32Store(nil, 2, 8), true},
		{"i64.load8_u", memory.InstI64Load8U(nil, 0, 0), true},
		{"memory.grow", memory.InstMemoryGrow(nil), true},
		{"memory.copy", memory.InstMemoryCopy(nil), true},
		{"memory.fill", memory.InstMemoryFill(nil), true},
		{"v128.load", simd.InstV128Load(nil, 4, 0), true},
		{"v128.store", simd.InstV128Store(nil, 4, 0), true},
		{"v128 lane op", simd.InstI8x16Bitmask(nil), false},
		{"nested blocks", func() []byte {
			b := inst.InstBlockStart(nil, inst.BlocktypeEmpty)
			b = inst.InstLoopStart(b, 0x7f)
			b = inst.InstI32Const(b, 0x3f)
			b = inst.InstBrIf(b, 0)
			b = inst.InstEnd(b)
			b = inst.InstEnd(b)
			return b
		}(), false},
		{"load buried in a block", func() []byte {
			b := inst.InstBlockStart(nil, inst.BlocktypeEmpty)
			b = inst.InstI32Const(b, 16)
			b = memory.InstI32Load(b, 2, 4)
			b = inst.InstDrop(b)
			b = inst.InstEnd(b)
			return b
		}(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codeUsesMemory(wrapBody(tc.body))
			if err != nil {
				t.Fatalf("codeUsesMemory: %v", err)
			}
			if got != tc.want {
				t.Errorf("codeUsesMemory = %v, want %v", got, tc.want)
			}
		})
	}
}

// A body the walker cannot decode has to be an error rather than a
// silent "no memory here": a guess would emit a module the validator
// rejects with "unknown memory 0", which is the failure this whole
// mechanism exists to prevent.
func TestCodeUsesMemoryRejectsUnknownOpcode(t *testing.T) {
	if _, err := codeUsesMemory(wrapBody([]byte{0xc5})); err == nil {
		t.Fatal("an unknown opcode decoded without error")
	}
	if _, err := codeUsesMemory(wrapBody([]byte{0x28})); err == nil {
		t.Fatal("a truncated memarg decoded without error")
	}
}

// Every runtime helper body must be walkable. The memory-section
// decision reads them all on every Emit, so an opcode the walker does
// not know would fail the build for any program that pulls the helper
// in — this finds it without needing such a program.
func TestEveryRuntimeHelperBodyDecodes(t *testing.T) {
	var names []string
	for name := range runtimeHelperSpecs {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no runtime helpers to walk")
	}
	// Cross-helper calls resolve through this map; an absent name
	// reads back as funcidx 0, which is fine for a decode-only walk.
	idxs := map[string]uint32{}
	// Two helpers carry no `body` in the table: their code is closed over
	// the string interner and emitted where the interner lives. The real
	// derivation still walks them, because it scans the assembled code
	// section rather than this table — but they cannot be reached from
	// here. Naming them keeps a NEW helper with a forgotten body failing
	// rather than quietly joining the exemption.
	bodyElsewhere := map[string]bool{
		"__fern_read_file": true,
		"__build_io_error": true,
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			spec := runtimeHelperSpecs[name]
			if spec.body == nil {
				if !bodyElsewhere[name] {
					t.Fatalf("%s has no body and is not one of the interner-closed helpers; "+
						"either give it a body or say here why it cannot have one", name)
				}
				return
			}
			if _, err := codeUsesMemory(spec.body(idxs)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		})
	}
	var overrides []string
	for name := range preview2HelperBodyOverrides {
		overrides = append(overrides, name)
	}
	sort.Strings(overrides)
	for _, name := range overrides {
		t.Run("preview2/"+name, func(t *testing.T) {
			if _, err := codeUsesMemory(preview2HelperBodyOverrides[name](idxs)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		})
	}
}

// A program whose own ops touch no memory but which pulls in a
// memory-touching runtime helper still needs the memory section: wasm
// validates the helper's body whether or not it ever runs, and a module
// without memory 0 behind it is rejected at instantiation. Here the
// reclaim helpers behind OpRcDec are the only thing that reads memory.
func TestMemorySectionForHelperOnlyMemoryUse(t *testing.T) {
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			// A short literal stays inline, so nothing here reaches
			// memory and no data segment is emitted; the reclaim
			// helper's body is the module's only memory use.
			{Kind: ir.OpConstStr, Str: "hi"},
			{Kind: ir.OpRcDec, Str: "__fern_str_dec"},
			{Kind: ir.OpConstI32, I32: 0},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if walkHasSection(t, bin, encode.SectionData) {
		t.Fatal("the literal was not inline — the module has a data segment, so it would need memory either way")
	}
	if !walkHasMemorySection(t, bin) {
		t.Fatal("no memory section in a module whose runtime helper reads memory")
	}
}
