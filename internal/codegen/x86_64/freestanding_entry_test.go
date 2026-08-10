package x86_64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
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
	// The `svc`-equivalent check from the issue's "done when". Asserted on
	// the emitted text here; the artifact-level disassembly check belongs
	// with the driver once freestanding builds end-to-end (#6511).
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
	// Allocates (so __fern_alloc is emitted) and exits (so __fern_exit is).
	src := `function main(): i32 {
	var xs: i32[] = [1, 2, 3];
	if (xs.len() == 0) { exit(1); }
	return 0;
}`
	asm := compileWithOptions(t, src, Options{Entry: platforms.EntryExports})

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
