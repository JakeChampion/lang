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

// TestTwoWordStringProducersLeaveAllocRc1SizeWord pins the other half of the
// __fern_str_dec contract (#6554): data-4 is the payload size __fern_alloc_rc1
// recorded, and the rc==1 free sizes the block from it. A producer that
// overwrites it with the string's LENGTH after asking for length + 1 (the
// trailing NUL) frees the block one 16-byte class low whenever the two round
// differently (len ≡ 8 mod 16) — the block is stranded below the class its
// re-allocation looks up and the leak census reads 16 phantom bytes per
// string. __fern_args, __fern_temp_dir, __fern_read_dir and
// __fern_remove_dir_all all did exactly that.
//
// Every two-word string producer is walked here: after each
// `bl __fern_alloc_rc1` the registers holding the returned data pointer are
// tracked until the next call, and a store to `[reg, #-4]` through one of
// them is the violation. Array-length prefixes (`__fern_args`' own header)
// follow a plain `bl __fern_alloc` and are not in the window.
func TestTwoWordStringProducersLeaveAllocRc1SizeWord(t *testing.T) {
	defer func(tw, rc bool) { ast.TwoWordOverride, ast.RcFreeEnabled = tw, rc }(ast.TwoWordOverride, ast.RcFreeEnabled)
	ast.TwoWordOverride = true
	ast.RcFreeEnabled = true

	type probe struct {
		emit func(*generator)
		syms []string
	}
	probes := []probe{
		{(*generator).emitStrcatRuntime, []string{"__fern_strcat"}},
		{(*generator).emitStringFromBytesRuntime, []string{"string_from_bytes_unchecked"}},
		{(*generator).emitStrSliceRuntime, []string{"__str_slice"}},
		{(*generator).emitEnvRuntime, []string{"__fern_env"}},
		{(*generator).emitStrBufRuntime, []string{"__fern_strbuf_take"}},
		{(*generator).emitArgsRuntime, []string{"__fern_args"}},
		{(*generator).emitReadLineRuntime, []string{"__fern_read_line"}},
		{(*generator).emitReadFileRuntime, []string{"__fern_read_file"}},
		{(*generator).emitTempDirRuntime, []string{"__fern_temp_dir"}},
		{(*generator).emitReadDirRuntime, []string{"__fern_read_dir"}},
		{(*generator).emitRemoveDirAllRuntime, []string{"__fern_remove_dir_all"}},
		{(*generator).emitReaderWriterRuntime, []string{"__fern_reader_read_line", "__fern_reader_read_chunk"}},
	}
	for _, p := range probes {
		g := &generator{stringLabel: map[string]string{}}
		p.emit(g)
		asm := g.out.String()
		for _, sym := range p.syms {
			body := helperBody(asm, sym)
			if body == "" {
				t.Fatalf("%s runtime was not emitted; cannot audit its allocation", sym)
			}
			if !strings.Contains(body, "bl __fern_alloc_rc1") {
				t.Errorf("%s does not allocate through __fern_alloc_rc1; the size-word audit has nothing to check", sym)
				continue
			}
			for _, hit := range allocRc1SizeWordStores(body) {
				t.Errorf("%s overwrites the payload size __fern_alloc_rc1 recorded at data-4 (`%s`): "+
					"__fern_str_dec frees with that word, so it must stay the size the producer requested",
					sym, hit)
			}
		}
	}
}

// allocRc1SizeWordStores returns every store to `[reg, #-4]` in body whose
// base register still holds a data pointer returned by the most recent
// `bl __fern_alloc_rc1`. Tracking is by plain data flow over the emitted
// text: x0 after the call, plus any register a `mov` copies it into, minus
// any register written by another instruction; the window closes at the
// next `bl`.
func allocRc1SizeWordStores(body string) []string {
	regX := func(r string) string {
		r = strings.TrimSuffix(r, ",")
		if strings.HasPrefix(r, "w") {
			return "x" + r[1:]
		}
		return r
	}
	var hits []string
	live := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasSuffix(f[0], ":") {
			continue
		}
		switch f[0] {
		case "bl":
			live = map[string]bool{}
			if f[1] == "__fern_alloc_rc1" {
				live["x0"] = true
			}
		case "mov":
			if len(f) >= 3 && live[regX(f[2])] {
				live[regX(f[1])] = true
			} else {
				delete(live, regX(f[1]))
			}
		case "str", "stur", "strb", "strh", "stp", "cmp", "cmn", "tst", "b", "b.ne", "b.eq", "b.lo", "b.hi", "b.ge", "b.gt", "b.le", "b.lt", "bge", "bgt", "ble", "blt", "bne", "beq", "cbz", "cbnz", "tbz", "tbnz", "svc", "ret":
			if (f[0] == "str" || f[0] == "stur") && len(f) >= 4 && f[3] == "#-4]" {
				base := regX(strings.TrimPrefix(f[2], "["))
				if live[base] {
					hits = append(hits, strings.TrimSpace(line))
				}
			}
		default:
			delete(live, regX(f[1]))
		}
	}
	return hits
}
