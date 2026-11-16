package e2ecompiler

import "testing"

// weakShared is a weak body as emit_function_via_ir_named writes it.
func weakShared(header, entry, tail string) string {
	return ".weak __fn_shared\n" + header +
		"__fn_shared:\n    .cfi_startproc\n" + entry +
		".Lssa_shared_1:\n    ret\n    .cfi_endproc\n" + tail
}

// unitOwn is the part of a unit that no other unit emits.
func unitOwn(own, tail string) string {
	return ".globl __fn_" + own + "\n__fn_" + own + ":\n    .cfi_startproc\n    ret\n    .cfi_endproc\n" + tail +
		"    .section .rodata\n.Lstr_" + own + ":\n    .ascii \"" + own + "\"\n    .text\n"
}

// The spawned per-module emit writes its units as one stream, so a weak body
// that two units emit must reach the stream once, and all else must stay.
func TestSelfHostWeakDedupe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/weak_dedupe_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/weak_dedupe_run.fern", "weak_dedupe_run")
	for _, tc := range []struct {
		target, header, entry, tail string
	}{
		{"x86-64-linux", ".weak __fn_shared.r\n", "    movq 8(%rsp), %rax\n__fn_shared.r:\n", ""},
		{"arm64-linux", ".weak __fn_shared.k\n.weak __fn_shared.r\n", "    ldr x0, [sp, #0]\n__fn_shared.k:\n__fn_shared.r:\n", "    .ltorg\n"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			shared := weakShared(tc.header, tc.entry, tc.tail)
			a := mustWrite(t, dir, tc.target+"_a.s", shared+unitOwn("a", tc.tail))
			b := mustWrite(t, dir, tc.target+"_b.s", shared+unitOwn("b", tc.tail))
			out, err := runX86_64Bin(runner, bin, a, b).CombinedOutput()
			if err != nil {
				t.Fatalf("weak_dedupe_run: %v\n%s", err, out)
			}
			if want := shared + unitOwn("a", tc.tail) + unitOwn("b", tc.tail); string(out) != want {
				t.Fatalf("deduped stream:\n%s\nwant:\n%s", out, want)
			}
		})
	}
}
