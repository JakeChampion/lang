package arm64

import (
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/platforms"
)

// allocatingSrc allocates (so __fern_alloc is emitted) and exits (so
// __fern_exit is), which between them pull in every runtime piece that
// capability gating cannot remove.
const allocatingSrc = `function main(): i32 {
	var xs: i32[] = [1, 2, 3];
	if (xs.len() == 0) { exit(1); }
	return 0;
}`

// entrySrc is a program with nothing in it but a return, so the only
// reason any of the process-entry runtime would appear is the entry
// shape itself.
const entrySrc = `function main(): i32 { return 0; }`

// TestArm64ProcessEntryEmitsStart pins the default: an unset Options.Entry
// means EntryProcess, so hosted targets keep emitting their entry symbol.
// Without this the entry gate could invert and every hosted binary would
// silently lose its entry point — a link failure, but only for whoever
// links it.
func TestArm64ProcessEntryEmitsStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"linux", Options{}, ".global _start"},
		{"darwin", Options{Darwin: true}, ".global _main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if asm := compile(t, entrySrc, tc.opts); !strings.Contains(asm, tc.want) {
				t.Errorf("default (zero-value) Options.Entry must emit %q: the hosted targets are entered there", tc.want)
			}
		})
	}
}

// TestArm64ExportsEntryOmitsProcessRuntime is the #6510 claim for arm64: a
// target whose artifact is entered by nobody emits no entry symbol, no
// process-stack read, and — for a program that reaches no gated builtin —
// no `svc` anywhere.
func TestArm64ExportsEntryOmitsProcessRuntime(t *testing.T) {
	asm := compile(t, entrySrc, Options{Entry: platforms.EntryExports})

	for _, sym := range []string{".global _start", ".global _main"} {
		if strings.Contains(asm, sym) {
			t.Errorf("EntryExports must not emit %q: nothing enters the artifact, and there is no process stack to read", sym)
		}
	}
	for _, slot := range []string{"__fern_argc", "__fern_argv", "__fern_envp"} {
		if strings.Contains(asm, slot) {
			t.Errorf("EntryExports must not capture %s: it is read off a process stack no kernel populated", slot)
		}
	}
	// The issue's "done when", at the emitted-text level. `svc` is not a
	// slow path on a target with no kernel — it is a fault.
	if strings.Contains(asm, "svc ") {
		t.Error("EntryExports emitted an `svc`: there is no kernel to service it")
	}
	// main itself must survive — the artifact IS its exported symbols.
	if !strings.Contains(asm, "main:") {
		t.Error("EntryExports dropped `main`; the exported symbols are the whole artifact")
	}
}

// TestArm64ExportsEntryTrapsInsteadOfSyscalling covers the three hosted
// fallbacks that are NOT reached by capability gating, so they cannot fall
// out for free the way `read_file` and the clocks do: the abort reporter
// and the allocator are emitted for programs that never name them, and
// `exit` is classified core precisely so every target keeps it. Each one
// stops the machine rather than issuing a syscall.
func TestArm64ExportsEntryTrapsInsteadOfSyscalling(t *testing.T) {
	asm := compile(t, allocatingSrc, Options{Entry: platforms.EntryExports})

	if strings.Contains(asm, "svc ") {
		t.Error("a hostless build must contain no `svc`; the abort reporter, allocator and exit paths all have to trap instead")
	}
	if !strings.Contains(asm, "brk #1") {
		t.Error("expected the hostless stop (`brk #1`) to appear; without it the traps above are not actually being emitted")
	}
	for _, sym := range []string{"__fern_report:", "__fern_exit:", "__fern_alloc:"} {
		if !strings.Contains(asm, sym) {
			t.Errorf("%s must still be defined — the call sites branch to it; only its body changes", sym)
		}
	}
	// The backtrace header is only written by the process-entry reporter,
	// so a hostless build should not carry the string at all.
	if strings.Contains(asm, "__fern_msg_bt") {
		t.Error("hostless build kept __fern_msg_bt, but nothing writes a backtrace without a stderr to write it to")
	}
}

// TestArm64HeapInitIsTheHostlessSeam is #6511: the heap stops being a
// region the runtime acquires and becomes one the embedder hands in.
// __fern_heap_init exists only off the process path — a hosted target
// still mmaps, and exporting a second way to seed its cursor would be two
// sources of truth for one allocator.
func TestArm64HeapInitIsTheHostlessSeam(t *testing.T) {
	hostless := compile(t, allocatingSrc, Options{Entry: platforms.EntryExports})
	if !strings.Contains(hostless, ".global __fern_heap_init") {
		t.Fatal("hostless build must export __fern_heap_init: with no mmap, a handed-in region is the only way to get a heap")
	}
	// It has to seed all three slots. Missing __fern_heap_end alone would
	// leave end == 0, so the very first bump compares past it and aborts.
	for _, slot := range []string{"__fern_heap_ptr", "__fern_heap_base", "__fern_heap_end"} {
		if !strings.Contains(hostless, slot) {
			t.Errorf("__fern_heap_init must seed %s", slot)
		}
	}

	if hosted := compile(t, allocatingSrc, Options{}); strings.Contains(hosted, "__fern_heap_init") {
		t.Error("hosted build must NOT export __fern_heap_init; it reserves its own region and the cursor has one owner")
	}
}

// TestArm64HostlessArtifactHasNoSVC is the issue's "done when" at the level
// it asks for: assemble the emitted text into real machine code and check
// the instruction stream, rather than grepping the asm. aarch64 is
// fixed-width, so decoding every word and masking for the SVC encoding is
// exact — no false positives, no false negatives.
func TestArm64HostlessArtifactHasNoSVC(t *testing.T) {
	asm := compile(t, allocatingSrc, Options{Entry: platforms.EntryExports})
	text, _, err := nativearm64.AssembleProgram(asm, 0x400000)
	if err != nil {
		t.Fatalf("hostless output must assemble (it is the artifact): %v", err)
	}
	if len(text) == 0 {
		t.Fatal("assembled .text is empty — the scan below would pass vacuously")
	}
	for off := 0; off+4 <= len(text); off += 4 {
		w := uint32(text[off]) | uint32(text[off+1])<<8 | uint32(text[off+2])<<16 | uint32(text[off+3])<<24
		// SVC #imm16 is 0xD4000001 | imm16<<5; mask out the immediate.
		if w&0xFFE0001F == 0xD4000001 {
			t.Fatalf("hostless artifact contains an `svc` at .text+%#x (word %#08x): on a target with no kernel that is a fault, not a slow path", off, w)
		}
	}
}
