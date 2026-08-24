package arm64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// TestTwoWordIoStringRuntimesUseRcHeaderedAlloc extends the #2817 guard to the
// I/O runtime helpers that also return an owned two-word string and were the
// same plain-__fern_alloc outliers: read_file (Ok(string)) and the Reader
// read_line / read_chunk (Some(string)).
//
// Each is reclaimed by __fern_str_dec, which reads the rc at data-8 and the
// payload size at data-4 — present only when the buffer is allocated through
// __fern_alloc_rc1. A plain __fern_alloc buffer has no such header, so the drop
// reads garbage and recycles a still-live cell (the #2817 heap-corruption class
// fixed for string_from_bytes_unchecked / env / read_line). These emit through stdlib /
// Reader types the in-package `compile` helper can't resolve, so we drive the
// runtime emitters directly with the two-word ABI active.
func TestTwoWordIoStringRuntimesUseRcHeaderedAlloc(t *testing.T) {
	defer func(tw, rc bool) { ast.TwoWordOverride, ast.RcFreeEnabled = tw, rc }(ast.TwoWordOverride, ast.RcFreeEnabled)
	ast.TwoWordOverride = true
	ast.RcFreeEnabled = true

	// Each emitter writes one or more `.global <sym>` helper bodies; map the
	// owned-string producers to the emitter that defines them.
	type probe struct {
		emit func(*generator)
		syms []string
	}
	probes := []probe{
		{(*generator).emitReadFileRuntime, []string{"__fern_read_file"}},
		{(*generator).emitReaderWriterRuntime, []string{"__fern_reader_read_line", "__fern_reader_read_chunk"}},
	}

	for _, p := range probes {
		g := &generator{stringLabel: map[string]string{}}
		p.emit(g)
		asm := g.out.String()
		for _, sym := range p.syms {
			body := helperBody(asm, sym)
			if body == "" {
				t.Fatalf("%s runtime was not emitted; cannot verify its allocation path", sym)
			}
			if !strings.Contains(body, "bl __fern_alloc_rc1") {
				t.Errorf("%s must allocate its owned string buffer via __fern_alloc_rc1 "+
					"(rc=1 at data-8, size at data-4) so __fern_str_dec reclaims it; a plain "+
					"__fern_alloc buffer has no rc header and corrupts the heap (#2817 class)\n--- body ---\n%s",
					sym, body)
			}
		}
	}
}
