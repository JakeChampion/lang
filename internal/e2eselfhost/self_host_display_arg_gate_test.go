package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostDisplayArgGate pins #5742 on the file-based x86 driver.
//
// `print` / `write` / `eprint` hand their argument to a string-only runtime
// helper that reads it as a `(ptr, len)` box. The self-host had no gate on the
// argument's type and no equivalent of native's Display spine (#2696, the
// `case "print", "write", "eprint"` block in internal/checker/checker.go), so
// two shapes compiled and ran with no diagnostic at all:
//
//   - `write(bs)` for a `u8[]` emitted a binary that wrote ZERO bytes. That is
//     how the issue was found: a fixture's decode half simply vanished from the
//     output while the encode half printed, so it read as a content mismatch
//     rather than a type error.
//   - `write(q)` for a struct carrying a `to_string` printed the struct's own
//     raw memory — the LENGTH read out of a field, so `Q { a: 7, b: 9 }` wrote
//     7 bytes of `Q \0 \0 \0 \0 \0 \0`. Native prints the `to_string()` result
//     for that same program.
//
// Both are silent wrong answers on programs the compiler accepted, which is
// the direction docs/NATIVE-CONVERGENCE.md calls out as the dangerous one: a
// self-host checker LESS strict than native lets bad programs through.
//
// The gate rejects any argument that is not already a string. For a Display
// type that is deliberately stricter than native, which auto-stringifies it —
// the self-host does not implement that rewrite, and a diagnostic naming the
// fix beats emitting a struct's raw bytes.
//
// The accept cases are the real risk: they pin that the gate never rejects a
// valid program, and that an accepted one still runs and prints.
func TestSelfHostDisplayArgGate(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	compile := func(t *testing.T, src string) ([]byte, []byte, int, string) {
		t.Helper()
		dir := t.TempDir()
		bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
		if err != nil {
			t.Fatalf("read builtins.fern: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "builtins.fern"), bsrc, 0o644); err != nil {
			t.Fatalf("write builtins.fern: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(src), 0o644); err != nil {
			t.Fatalf("write main.fern: %v", err)
		}
		out, errOut, code := runDriverAllowFail(t, runner, driverBin, "", filepath.Join(dir, "main.fern"))
		return out, errOut, code, dir
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
			[]string{"E038", "write"},
		},
		// print and eprint share the helper, so they shared the hole.
		{
			"print-u8-array",
			"function main(): i32 {\n    var b: u8[] = [72 as u8, 105 as u8];\n    print(b);\n    return 0;\n}\n",
			[]string{"E038", "print"},
		},
		{
			"eprint-u8-array",
			"function main(): i32 {\n    var b: u8[] = [72 as u8, 105 as u8];\n    eprint(b);\n    return 0;\n}\n",
			[]string{"E038", "eprint"},
		},
		// A scalar with no to_string in scope: native raises E038 here too.
		{
			"write-i32",
			"function main(): i32 {\n    var n: i32 = 5;\n    write(n);\n    return 0;\n}\n",
			[]string{"E038", "i32"},
		},
		// The worse half: native COMPILES this and prints the to_string()
		// result. The self-host printed the struct's raw bytes instead, with
		// the byte count coming from `a`. The diagnostic names the type.
		{
			"write-struct-with-to-string",
			"struct Q { a: i32, b: i32 }\n" +
				"function (q: Q) to_string(): string { return \"HELLO\"; }\n" +
				"function main(): i32 {\n    var q: Q = Q { a: 7, b: 9 };\n    write(q);\n    return 0;\n}\n",
			[]string{"E038", "Q"},
		},
		// Nested in a branch: bare expression statements were hitting
		// check_stmt's `_ =>` catch-all and never being visited, so the walk
		// must reach one inside a body.
		{
			"nested-in-if",
			"function main(): i32 {\n    var b: u8[] = [1 as u8];\n    if (b.len() > 0) { write(b); }\n    return 0;\n}\n",
			[]string{"E038", "write"},
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
    print("done");
    return 0;
}
`
	t.Run("accept", func(t *testing.T) {
		asm, errOut, code, dir := compile(t, acceptSrc)
		if code != 0 {
			t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
		}
		bin := buildBin(t, gcc, dir, "accept", string(asm))
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
		if want := "litlocloc-catlabQ!done\n"; out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	})

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
