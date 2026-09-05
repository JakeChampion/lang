package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// The strbuf runtime had two defects, both found while scoping #2649's
// remaining migration targets. They are independent of whether the helpers ever
// become Fern; these tests pin the fixes.
//
// (1) `__fern_strbuf_take` built its result with a bare `__fern_alloc(16)`, so
// the returned box carried NO refcount header, where every other self-host
// string box is the 24-byte `{rc@base, data@base+8, len@base+16}` block
// `__fern_str_box` builds. Unlike the Reader leaves of #6921 — which leaked
// because nothing ever dec'd them — `strbuf_take()` IS treated as a fresh owned
// string (irlower's str-tracking), so a dropped result reaches
// `__fn___fern_str_free`, which reads the refcount at box-8. On a headerless box
// that is the last word of the PRECEDING allocation, which here is the tail of
// the text just copied out of the accumulator. The native backend has built this
// one with an rc-headered allocation since docs/RC-STRINGS-PLAN.md; the
// self-host was the last producer left unconverted.
//
// (2) arm64 emitted the whole bundle (the `.bss` words and all three bodies)
// inside the bare `heap` gate, where x86-64 has always gated it on the strbuf
// need. So every allocating arm64 program carried three bodies nothing
// branched to.

// strbufTakeAsm compiles a strbuf program for `target` on the self-host IR path
// and returns the emitted assembly.
func strbufTakeAsm(t *testing.T, target string) string {
	t.Helper()
	const src = `function main(): i32 {
    strbuf_reset();
    strbuf_append("ab");
    var s: string = strbuf_take();
    return s.len();
}`
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	args := []string{}
	if target != "" {
		args = append(args, "-target", target)
	}
	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(src+"\n"), args...))
	if len(asm) == 0 {
		t.Fatalf("self-host compiler emitted 0 bytes for target %q", target)
	}
	return asm
}

// TestSelfHostStrbufTakeIsRcHeaded pins defect (1) on both register backends:
// the drained string comes from __fern_str_box, so it carries the rc header at
// box-8 that __fn___fern_str_free reads.
func TestSelfHostStrbufTakeIsRcHeaded(t *testing.T) {
	for _, tc := range []struct {
		name, target, call, bareBox string
	}{
		// The bare-box spelling is the exact instruction pair the defect emitted
		// between the copy loop and the reset, so its absence is the assertion
		// that the old body is gone rather than merely joined by a new one.
		{"x86_64", "", "call __fern_str_box", "movq $16, %rdi\n    call __fern_alloc"},
		{"arm64", "arm64-linux", "bl __fern_str_box", "mov x0, #16\n    bl __fern_alloc\n    str x20, [x0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := strbufTakeAsm(t, tc.target)
			take := strbufTakeBody(t, asm)
			if !strings.Contains(take, tc.call) {
				t.Errorf("__fern_strbuf_take does not box through __fern_str_box; body:\n%s", take)
			}
			if strings.Contains(take, tc.bareBox) {
				t.Errorf("__fern_strbuf_take still builds a headerless box with a bare __fern_alloc; body:\n%s", take)
			}
		})
	}
}

// strbufTakeBody slices out just the __fern_strbuf_take body, so a match cannot
// come from a neighbouring helper. Scoping matters here: `bl __fern_str_box`
// appears all over the runtime, so an unscoped Contains would pass against the
// defect.
func strbufTakeBody(t *testing.T, asm string) string {
	t.Helper()
	start := strings.Index(asm, "__fern_strbuf_take:\n")
	if start < 0 {
		t.Fatal("emitted asm has no __fern_strbuf_take body")
	}
	rest := asm[start:]
	// Both backends end the body with a `ret`; take through the first one after
	// the copy loop's own backward branch.
	end := strings.Index(rest, "\n    ret\n")
	if end < 0 {
		t.Fatal("__fern_strbuf_take body has no ret")
	}
	return rest[:end]
}

// TestSelfHostStrbufNeedGatedArm64 pins defect (2): a heap-using program that
// never touches the string-builder must not reserve its .bss words or emit
// its bodies. Asserted in both directions so the gate cannot be vacuous.
func TestSelfHostStrbufNeedGatedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// Allocates (a heap string array) but never uses the string-builder.
	const noStrbuf = `function main(): i32 {
    var xs: string[] = ["a", "b"];
    return xs.len() - 2;
}`
	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(noStrbuf+"\n"), "-target", "arm64-linux"))
	if !strings.Contains(asm, "__fern_alloc") {
		t.Fatal("the no-strbuf program did not emit the heap runtime — the gate under test is vacuous")
	}
	if strings.Contains(asm, "__fern_strbuf_ptr") {
		t.Error("a heap-using program that never uses the string-builder still reserves its .bss words")
	}
	if strings.Contains(asm, "__fern_strbuf_take:") {
		t.Error("a heap-using program that never uses the string-builder still emits __fern_strbuf_take")
	}

	// The other direction: a program that DOES use it still gets the bundle.
	const usesStrbuf = `function main(): i32 {
    strbuf_reset();
    strbuf_append("ab");
    return strbuf_take().len() - 2;
}`
	asm2 := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(usesStrbuf+"\n"), "-target", "arm64-linux"))
	for _, want := range []string{"__fern_strbuf_ptr", "__fern_strbuf_take:", "__fern_strbuf_append:", "__fern_strbuf_reset:", "__fern_strbuf_grow:"} {
		if !strings.Contains(asm2, want) {
			t.Errorf("a strbuf-using arm64 program is missing %q — the need is not reaching the bundle", want)
		}
	}
}

// TestSelfHostStrbufTakeReclaimLoop guards the OTHER direction of defect (1):
// the fix turns a dec that read a garbage word into one that really frees, so
// each of these 5000 drained-and-dropped strings now goes back through the
// size-class freelist and is handed out again on the next round. A bad free
// would corrupt a bin long before the loop ends.
//
// It is a regression guard, not a reproducer — it passes against the defect
// too, because whether the headerless box corrupts anything depends on what
// happens to sit in the preceding allocation's last word. The two asm assertions
// above are what fail on the unfixed emitter.
func TestSelfHostStrbufTakeReclaimLoop(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    var i: i32 = 0;
    var last: i32 = 0;
    while (i < 5000) {
        strbuf_reset();
        strbuf_append("abcdefgh");
        strbuf_append("ijklmnop");
        var s: string = strbuf_take();
        last = s.len();
        i = i + 1;
    }
    return last;
}`
	asm := string(runCapture(t, gcc, runner, driverBin, []byte(src+"\n"), "-ir"))
	progBin := buildBin(t, gcc, dir, "strbuf_reclaim", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 16 {
		t.Errorf("strbuf take/drop loop exited %d, want 16 (the last drained length)", got)
	}
}

// TestSelfHostMapRuntimeNeedGatedArm64 pins the same defect shape one bundle
// over. arm64 marked `maps` at every map op site and then never read it: the
// seven-helper bundle was keyed on `arr_push && str_eq` instead, which are
// `maps`'s own declared dependencies. That proxy can never UNDER-emit, so it
// was never a link failure — it over-emits, and any program that appends to an
// array and compares two strings carried the whole map runtime with no map in
// it. x86-64 has gated on `has_need("maps")` all along.
func TestSelfHostMapRuntimeNeedGatedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// Appends to an array AND compares two strings — the exact proxy condition
	// the old gate keyed on — with no map anywhere.
	const noMap = `function main(): i32 {
    var xs: string[] = [];
    xs = xs.append("a");
    xs = xs.append("b");
    if (xs[0] == xs[1]) { return 1; }
    return xs.len() - 2;
}`
	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(noMap+"\n"), "-target", "arm64-linux"))
	for _, proxy := range []string{"__fern_arr_push", "__fn___fern_str_eq"} {
		if !strings.Contains(asm, proxy) {
			t.Fatalf("the no-map program did not emit %s — the proxy condition is not met, so this test proves nothing", proxy)
		}
	}
	for _, unwanted := range []string{"__fern_map_new:", "__fern_map_set:", "__fern_map_delete:"} {
		if strings.Contains(asm, unwanted) {
			t.Errorf("a program with no map still emits %s", unwanted)
		}
	}

	// The other direction: a map-using program still gets the bundle.
	const usesMap = `function main(): i32 {
    var m: Map[string, i32] = Map {};
    m = m.insert("k", 7);
    return m.get_or("k", 0) - 7;
}`
	asm2 := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(usesMap+"\n"), "-target", "arm64-linux"))
	for _, want := range []string{"__fern_map_new:", "__fern_map_set:", "__fern_map_get:"} {
		if !strings.Contains(asm2, want) {
			t.Errorf("a map-using arm64 program is missing %q — the need is not reaching the bundle", want)
		}
	}
}
