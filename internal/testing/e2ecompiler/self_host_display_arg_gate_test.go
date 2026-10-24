package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostDisplayArgGate(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	compile := func(t *testing.T, src string) ([]byte, []byte, int, string) {
		t.Helper()
		dir := writeSourceModloadProject(t, src)
		out, errOut, code := runDriverAllowFail(t, runner, driverBin, "", filepath.Join(dir, "main.fern"))
		return out, errOut, code, dir
	}

	noDisplay := func(fn, ty string) string {
		return "argument 1 to \"" + fn + "\": " + ty + " does not implement `Display` (no `to_string(): string` in scope)"
	}
	rejects := []struct {
		name string
		src  string
		want []string
	}{
		{
			"write-u8-array",
			"function main(): i32 {\n    let b: u8[] = [72 as u8, 105 as u8];\n    write(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("write", "u8[]")},
		},
		{
			"print-u8-array",
			"function main(): i32 {\n    let b: u8[] = [72 as u8, 105 as u8];\n    print(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "u8[]")},
		},
		{
			"eprint-u8-array",
			"function main(): i32 {\n    let b: u8[] = [72 as u8, 105 as u8];\n    eprint(b);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("eprint", "u8[]")},
		},
		{
			"write-i32",
			"function main(): i32 {\n    let n: i32 = 5;\n    write(n);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("write", "i32"), "`import \"core/cmp\";`"},
		},
		{
			"print-f64",
			"function main(): i32 {\n    let v: f64 = 1.5;\n    print(v);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "f64")},
		},
		{
			"print-i32-array-with-cmp-imported",
			"import \"core/cmp\";\nfunction main(): i32 {\n    let xs: i32[] = [1, 2];\n    print(xs);\n    return 0;\n}\n",
			[]string{"E038", noDisplay("print", "i32[]")},
		},
		{
			"nested-in-if",
			"function main(): i32 {\n    let b: u8[] = [1 as u8];\n    if (b.len() > 0) { write(b); }\n    return 0;\n}\n",
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

	const acceptSrc = `struct Q { a: i32 }
function (q: Q) to_string(): string { return "Q!"; }
function label(): string { return "lab"; }
function main(): i32 {
    let q: Q = Q { a: 1 };
    let s: string = "loc";
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
	const primitivesSrc = `import "core/cmp";
function main(): i32 {
    let a: i32 = 7; let b: i64 = 7i64; let c: u32 = 7 as u32; let d: u64 = 7 as u64;
    let e: f64 = 1.5; let f: boolean = true; let g: u8 = 65 as u8;
    print(a); print(b); print(c); print(d); print(e); print(f); print(g);
    print(7); print(1 + 2);
    write(a); write("|"); eprint(a);
    return 0;
}
`
	const shadowSrc = `function (n: i32) to_string(): string { return "SHADOW"; }
function main(): i32 {
    print(5);
    let m: i32 = 6; print(m.to_string());
    return 0;
}
`
	const booleanSrc = `function main(): i32 {
    let b: boolean = true;
    print(b);
    print(false);
    return 0;
}
`
	accepts := []struct {
		name, src, want string
	}{
		{"accept", acceptSrc, "litlocloc-catlabQ!Q!done\n"},
		{"accept-primitives", primitivesSrc, "7\n7\n7\n7\n1.5\ntrue\n65\n7\n3\n7|"},
		{"accept-boolean-without-import", booleanSrc, "true\nfalse\n"},
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

	t.Run("accept-stdlib", func(t *testing.T) {
		asm, _ := compileSourceModload(t, runner, driverBin,
			"import \"std/i32\";\nfunction main(): i32 { let n: i32 = 7; print(n.to_string()); return 0; }\n")
		if !strings.Contains(asm, "__fn_main") {
			t.Fatalf("stdlib program emitted no main (%d bytes)", len(asm))
		}
	})
}
