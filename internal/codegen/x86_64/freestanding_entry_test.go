package x86_64

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// compileWithOptions is `compile` with the emit options exposed, for the
// entry-shape tests. The shared helper takes none.
func compileWithOptions(t *testing.T, src string, opts Options) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := EmitWithOptions(prog, info, opts)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

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

// TestX8664ProcessEntryEmitsStart pins the default: an unset Options.Entry
// means EntryProcess, so hosted targets keep emitting `_start`. Without
// this the entry gate could invert and every hosted binary would silently
// lose its entry point — a link failure, but only for whoever links it.
func TestX8664ProcessEntryEmitsStart(t *testing.T) {
	asm := compileWithOptions(t, entrySrc, Options{})
	if !strings.Contains(asm, ".globl _start") {
		t.Error("default (zero-value) Options.Entry must emit `_start`: the hosted targets are entered there")
	}
}

// TestX8664ExportsEntryOmitsProcessRuntime is the #6510 claim for x86-64:
// a target whose artifact is entered by nobody emits no entry symbol, no
// process-stack read, and — for a program that reaches no gated builtin —
// no syscall instruction at all.
func TestX8664ExportsEntryOmitsProcessRuntime(t *testing.T) {
	asm := compileWithOptions(t, entrySrc, Options{Entry: platforms.EntryExports})

	if strings.Contains(asm, ".globl _start") {
		t.Error("EntryExports must not emit `_start`: nothing enters the artifact, and there is no process stack to read")
	}
	for _, slot := range []string{"__fern_argc", "__fern_argv", "__fern_envp"} {
		if strings.Contains(asm, slot) {
			t.Errorf("EntryExports must not capture %s: it is read off a process stack no kernel populated", slot)
		}
	}
	// Emitted-text form of the no-syscall claim;
	// TestX8664HostlessArtifactHasNoSyscall makes it against assembled
	// machine code.
	if strings.Contains(asm, "syscall") {
		t.Error("EntryExports emitted a syscall instruction: there is no kernel to service it")
	}
	// main itself must survive — the artifact IS its exported symbols.
	if !strings.Contains(asm, "main:") {
		t.Error("EntryExports dropped `main`; the exported symbols are the whole artifact")
	}
}

// TestX8664ExportsEntryTrapsInsteadOfSyscalling covers the three hosted
// fallbacks that are NOT reached by capability gating, so they cannot fall
// out for free the way `read_file` and the clocks do: the abort reporter
// and the allocator are emitted for programs that never name them, and
// `exit` is classified core precisely so every target keeps it. Each one
// stops the machine rather than issuing a syscall.
func TestX8664ExportsEntryTrapsInsteadOfSyscalling(t *testing.T) {
	asm := compileWithOptions(t, allocatingSrc, Options{Entry: platforms.EntryExports})

	if strings.Contains(asm, "syscall") {
		t.Error("a hostless build must contain no syscall; the abort reporter, allocator and exit paths all have to trap instead")
	}
	if !strings.Contains(asm, "ud2") {
		t.Error("expected the hostless stop (`ud2`) to appear; without it the traps above are not actually being emitted")
	}
	for _, sym := range []string{"__fern_report:", "__fern_exit:", "__fern_alloc:"} {
		if !strings.Contains(asm, sym) {
			t.Errorf("%s must still be defined — the call sites jmp to it; only its body changes", sym)
		}
	}
	// The backtrace header is only written by the process-entry reporter,
	// so a hostless build should not carry the string at all.
	if strings.Contains(asm, "__fern_msg_bt") {
		t.Error("hostless build kept __fern_msg_bt, but nothing writes a backtrace without a stderr to write it to")
	}
}

// TestX8664HeapInitIsTheHostlessSeam is #6511: the heap stops being a
// region the runtime acquires and becomes one the embedder hands in.
// __fern_heap_init exists only off the process path — a hosted target
// still mmaps, and exporting a second way to seed its cursor would be two
// sources of truth for one allocator.
func TestX8664HeapInitIsTheHostlessSeam(t *testing.T) {
	hostless := compileWithOptions(t, allocatingSrc, Options{Entry: platforms.EntryExports})
	if !strings.Contains(hostless, ".globl __fern_heap_init") {
		t.Fatal("hostless build must export __fern_heap_init: with no mmap, a handed-in region is the only way to get a heap")
	}
	// It has to seed all three slots. Missing __fern_heap_end alone would
	// leave end == 0, so the very first bump compares past it and aborts.
	for _, slot := range []string{"__fern_heap_ptr", "__fern_heap_base", "__fern_heap_end"} {
		if !strings.Contains(hostless, slot) {
			t.Errorf("__fern_heap_init must seed %s", slot)
		}
	}

	if hosted := compileWithOptions(t, allocatingSrc, Options{}); strings.Contains(hosted, "__fern_heap_init") {
		t.Error("hosted build must NOT export __fern_heap_init; it reserves its own region and the cursor has one owner")
	}
}

// TestX8664HostlessArtifactHasNoSyscall is the issue's "done when" at the
// level it asks for: assemble the emitted text into machine code and check
// the instruction stream rather than grepping the asm.
//
// x86 is variable-length, so scanning for the two-byte 0F 05 without
// decoding could in principle match bytes inside an immediate or a
// displacement. That error only runs one way: a stray match FAILS the
// test, it never hides a real syscall. So a pass is conclusive, which is
// the direction that matters here.
func TestX8664HostlessArtifactHasNoSyscall(t *testing.T) {
	asm := compileWithOptions(t, allocatingSrc, Options{Entry: platforms.EntryExports})
	text, _, err := nativex86.AssembleProgram(asm, 0x400000)
	if err != nil {
		t.Fatalf("hostless output must assemble (it is the artifact): %v", err)
	}
	if len(text) == 0 {
		t.Fatal("assembled .text is empty — the scan below would pass vacuously")
	}
	if i := bytes.Index(text, []byte{0x0F, 0x05}); i >= 0 {
		t.Fatalf("hostless artifact contains the `syscall` opcode at .text+%#x: there is no kernel to service it", i)
	}
}
