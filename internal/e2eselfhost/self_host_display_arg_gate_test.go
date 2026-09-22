package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostDisplayArgGate pins the Display spine (docs/TRAITS.md §3a) on
// the file-based x86 driver.
//
// `print` / `write` / `eprint` hand their argument to a string-only runtime
// helper that reads it as a `(ptr, len)` box. Before the gate (#5742) the
// self-host had no equivalent of native's Display spine (#2696, the
// `case "print", "write", "eprint"` block in internal/checker/checker.go), so
// `write(bs)` for a `u8[]` wrote ZERO bytes and `write(q)` for a struct wrote
// its own raw memory — silent wrong answers on programs the compiler
// accepted, the direction docs/NATIVE-CONVERGENCE.md calls the dangerous one.
//
// The self-host checker now does what native's does, at the same point: an
// argument whose type resolves `to_string(): string` is rewritten to
// `arg.to_string()` (checker.display_rewrite, in annotate_module), and one
// whose type does not is refused with native's E038 text
// (checker.display_arg_diags). This driver runs no checker gate of its own;
// the emitter enters the annotate pass before its pre-codegen check, and that
// check (asmcore.display_arg_errs) refuses what the checker left unrewritten
// in the same words, so the rejects below read as native's do. A scalar
// resolves it through the stdlib method the import closure brings in —
// `core/cmp` pulls std/i32, std/i64, std/u32, std/u64 and std/float and
// carries the boolean and u8 impls — so `print(7)` compiles with the import
// and is refused without it, as it is on native (#9945). A program's own
// `to_string` on a primitive is the method called, as on native: the lowering
// no longer bypasses a declared method for the built-in formatter.
//
// The accept cases are the real risk: they pin that the gate never rejects a
// valid program, and that an accepted one runs and prints what native prints.
func TestSelfHostDisplayArgGate(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// compile lays the program and its stdlib closure out the way the
	// file-based driver loads them and runs the driver on it.
	compile := func(t *testing.T, src string) ([]byte, []byte, int, string) {
		t.Helper()
		dir := writeSourceModloadProject(t, src)
		out, errOut, code := runDriverAllowFail(t, runner, driverBin, "", filepath.Join(dir, "main.fern"))
		return out, errOut, code, dir
	}

	// native's E038 text, which display_arg_diags reproduces word for word.
	noDisplay := func(fn, ty string) string {
		return "argument 1 to " + fn + ": " + ty + " does not implement `Display` (no `to_string(): string` in scope)"
	}
	rejects := []struct {
		name string
		src  string
		want []string
	}{
		// The issue's repro, verbatim. Before the gate: exit 0, 582 lines of
		// asm, and a binary that wrote 0 bytes.
		{
			"write-u8-array",
			"function main(): i32 {\n    var b: u8[] = [72 as u8, 105 as u8];\n    write(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("write", "u8[]")},
		},
		// print and eprint share the helper, so they shared the hole.
		{
			"print-u8-array",
			"function main(): i32 {\n    var b: u8[] = [72 as u8, 105 as u8];\n    print(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "u8[]")},
		},
		{
			"eprint-u8-array",
			"function main(): i32 {\n    var b: u8[] = [72 as u8, 105 as u8];\n    eprint(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("eprint", "u8[]")},
		},
		// A scalar with no to_string in scope: native raises E038 here too,
		// naming the import that brings the method in.
		{
			"write-i32",
			"function main(): i32 {\n    var n: i32 = 5;\n    write(n);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("write", "i32"), "`import \"core/cmp\";`"},
		},
		{
			"print-boolean",
			"function main(): i32 {\n    var b: boolean = true;\n    print(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "boolean")},
		},
		{
			"print-f64",
			"function main(): i32 {\n    var v: f64 = 1.5;\n    print(v);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "f64")},
		},
		// An array stays refused with the import: nothing gives `i32[]` a
		// to_string.
		{
			"print-i32-array-with-cmp-imported",
			"import \"core/cmp\";\nfunction main(): i32 {\n    var xs: i32[] = [1, 2];\n    print(xs);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "i32[]")},
		},
		// Nested in a branch: bare expression statements were hitting
		// check_stmt's `_ =>` catch-all and never being visited, so the walk
		// must reach one inside a body.
		{
			"nested-in-if",
			"function main(): i32 {\n    var b: u8[] = [1 as u8];\n    if (b.len() > 0) { write(b); }\n    return 0;\n}\n",
			[]string{"E038", noDisplay("write", "u8[]")},
		},
	}

	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code, _ := compile(t, tc.src)
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(errOut), want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for a non-string print argument, want 0", len(out))
			}
		})
	}

	// The accept program walks the string-valued shapes a real program uses:
	// a literal, a string local, a concatenation, a call returning string, a
	// receiver method returning string, and an explicit .to_string() — the
	// spelling the diagnostic tells the user to write.
	const acceptSrc = `struct Q { a: i32 }
function (q: Q) to_string(): string { return "Q!"; }
function label(): string { return "lab"; }
function main(): i32 {
    var q: Q = Q { a: 1 };
    var s: string = "loc";
    write("lit");
    write(s);
    write(s + "-cat");
    write(label());
    write(q.to_string());
    write(q);
    eprint(q);
    print("done");
    return 0;
}
`
	// Every primitive `print` accepts on native, with the import that brings
	// its `to_string` into scope: the stdlib method for the integers and f64,
	// core/cmp's own impls for boolean and u8, and a literal and an arithmetic
	// result beside the locals. The expected text is what native prints.
	const primitivesSrc = `import "core/cmp";
function main(): i32 {
    var a: i32 = 7; var b: i64 = 7i64; var c: u32 = 7 as u32; var d: u64 = 7 as u64;
    var e: f64 = 1.5; var f: boolean = true; var g: u8 = 65 as u8;
    print(a); print(b); print(c); print(d); print(e); print(f); print(g);
    print(7); print(1 + 2);
    write(a); write("|"); eprint(a);
    return 0;
}
`
	// A program's own to_string on a primitive is the method the call
	// dispatches to, on native and here — not the built-in formatter.
	const shadowSrc = `function (n: i32) to_string(): string { return "SHADOW"; }
function main(): i32 {
    print(5);
    var m: i32 = 6; print(m.to_string());
    return 0;
}
`
	accepts := []struct {
		name, src, want string
	}{
		{"accept", acceptSrc, "litlocloc-catlabQ!Q!done\n"},
		{"accept-primitives", primitivesSrc, "7\n7\n7\n7\n1.5\ntrue\n65\n7\n3\n7|"},
		{"accept-shadowing-to-string", shadowSrc, "SHADOW\nSHADOW\n"},
	}
	for _, tc := range accepts {
		t.Run(tc.name, func(t *testing.T) {
			asm, errOut, code, dir := compile(t, tc.src)
			if code != 0 {
				t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			out, exit := runBin(cmd, "")
			if exit != 0 {
				t.Errorf("program exited %d, want 0 (stdout %q)", exit, out)
			}
			if out != tc.want {
				t.Errorf("stdout = %q, want %q", out, tc.want)
			}
		})
	}

	// A stdlib-importing program is the false-positive case that matters
	// most: its call sites are mangled, and the argument's type has to
	// survive that for the gate to judge it rather than mis-flag it.
	t.Run("accept-stdlib", func(t *testing.T) {
		asm, _ := compileSourceModload(t, runner, driverBin,
			"import \"std/i32\";\nfunction main(): i32 { var n: i32 = 7; print(n.to_string()); return 0; }\n")
		if !strings.Contains(asm, "__fn_main") {
			t.Fatalf("stdlib program emitted no main (%d bytes)", len(asm))
		}
	})
}
