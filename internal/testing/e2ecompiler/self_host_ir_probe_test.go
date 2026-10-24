package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type irProbeCase struct {
	name        string
	src         string
	wantVerdict string
	wantLines   []string
}

func irProbeCases() []irProbeCase {
	return []irProbeCase{
		{
			name:        "pure-i32",
			src:         "@noinline function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(2, 3); }",
			wantVerdict: "module: IR",
			wantLines:   []string{"add: ir", "main: ir"},
		},
		{
			name:        "unknown-call-fails-the-checks",
			src:         "function main(): i32 { return mystery(3); }",
			wantVerdict: "module: refused",
			wantLines:   []string{"mystery"},
		},
		{
			name:        "typed-refusal-names-its-reason",
			src:         viewTemplateSrc,
			wantVerdict: "module: refused",
			wantLines:   []string{"main: ir", "first$str: refused: a view is lent, never retained"},
		},
		{
			name:        "no-main-is-ir",
			src:         "function helper(): i32 { return 1; }",
			wantVerdict: "module: IR",
			wantLines:   []string{"helper: ir"},
		},
		{
			name:        "break-continue-array-for-lowers",
			src:         "function main(): i32 {\n let acc: i32 = 0;\n let xs: i32[] = [1, 2, 3, 4, 5];\n for x in xs { if (x == 2) { continue; } if (x == 4) { break; } acc = acc + x; }\n return acc;\n}",
			wantVerdict: "module: IR",
			wantLines:   []string{"main: ir"},
		},
		{
			name:        "method-receiver-lowers",
			src:         "struct P { x: i32 }\nfunction (p: P) get(): i32 { return p.x; }\nfunction main(): i32 { let p = P { x: 7 }; return p.get(); }",
			wantVerdict: "module: IR",
			wantLines:   []string{"P.get: ir", "main: ir"},
		},
		{
			name:        "generic-template-and-instances",
			src:         `function id[T](x: T): T { return x; } function main(): i32 { let s = id("hi"); return id(s.len()); }`,
			wantVerdict: "module: IR",
			wantLines:   []string{"id: template", "main: ir"},
		},
		{
			name:        "bodyless-import",
			src:         `@import("test", "external") function external(x: i32): i32; function main(): i32 { return external(7); }`,
			wantVerdict: "module: IR",
			wantLines:   []string{"external: extern", "main: ir"},
		},
	}
}

func TestSelfHostIREligibilityProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "airun")

	probe := func(t *testing.T, prog string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir-probe")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir-probe")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q: %v", prog, err)
		}
		return string(out)
	}

	cases := irProbeCases()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := probe(t, c.src)
			if !strings.HasSuffix(rep, c.wantVerdict+"\n") {
				t.Errorf("verdict: report missing %q\n--- report ---\n%s", c.wantVerdict, rep)
			}
			for _, line := range c.wantLines {
				if !strings.Contains(rep, line) {
					t.Errorf("report missing line %q\n--- report ---\n%s", line, rep)
				}
			}
		})
	}
}

func TestSelfHostIRPipelineProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	probe := func(t *testing.T, prog, root string) string {
		t.Helper()
		entry := filepath.Join(dir, "probe_entry.fern")
		if err := os.WriteFile(entry, []byte(prog), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		args := []string{entry}
		if root != "" {
			args = append(args, root)
			args = append(args, "-no-treeshake")
		}
		args = append(args, "-ir-probe")
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q: %v", prog, err)
		}
		return string(out)
	}

	t.Run("self-contained", func(t *testing.T) {
		rep := probe(t, "@noinline function helper(n: i32): i32 { return n * 2; }\nfunction main(): i32 { return helper(21); }", "")
		for _, want := range []string{"helper: ir", "main: ir", "module: IR"} {
			if !strings.Contains(rep, want) {
				t.Errorf("report missing %q\n--- report ---\n%s", want, rep)
			}
		}
	})

	decide := func(t *testing.T, prog string) string {
		t.Helper()
		entry := filepath.Join(dir, "decide_entry.fern")
		if err := os.WriteFile(entry, []byte(prog), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		out, err := runX86_64Bin(runner, driverBin, entry, "-decide").Output()
		if err != nil {
			t.Fatalf("decide failed for %q: %v", prog, err)
		}
		return strings.TrimSpace(string(out))
	}

	t.Run("decide-agrees-with-the-probe", func(t *testing.T) {
		produced := "@noinline function helper(n: i32): i32 { return n * 2; }\nfunction main(): i32 { return helper(21); }"
		if got := decide(t, produced); got != "ir" {
			t.Errorf("-decide on a module produced whole = %q, want \"ir\"", got)
		}
		rep := probe(t, viewTemplateSrc, "")
		if !strings.Contains(rep, "first$str: refused: a view is lent, never retained") || !strings.HasSuffix(rep, "module: refused\n") {
			t.Errorf("probe of a refused module\n--- report ---\n%s", rep)
		}
		if got := decide(t, viewTemplateSrc); got != "refused" {
			t.Errorf("-decide on a refused module = %q, want \"refused\"", got)
		}
	})

	t.Run("stdlib-loaded-whole-program", func(t *testing.T) {
		rep := probe(t, "import \"std/array\";\nfunction main(): i32 { let xs: i32[] = [1, 2, 3]; return xs.len(); }", stdlibRoot)
		if !strings.Contains(rep, "module:") {
			t.Errorf("report missing verdict line\n--- report ---\n%s", rep)
		}
		if !strings.Contains(rep, "array__") {
			t.Errorf("report does not list mangled std/array functions — stdlib not loaded into the probe?\n--- report (head) ---\n%s", firstNLines(rep, 20))
		}
	})
}

func TestSelfHostPathProbePrintsRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_pathprobe_run.fern")
	probe := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")
	for _, c := range irProbeCases() {
		t.Run(c.name, func(t *testing.T) {
			want := "refused"
			if c.wantVerdict == "module: IR" {
				want = "ir"
			}
			if got := strings.TrimSpace(string(runCapture(t, gcc, runner, probe, []byte(c.src)))); got != want {
				t.Errorf("path probe = %q, want %q", got, want)
			}
		})
	}
}

func TestSelfHostVerdictBoolean(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_pathprobe_run.fern")
	const src = `import "../parser";
import "./rundriver";
import "../semlower";
function main(): i32 {
  let mod: parser.Module = rundriver.parse_stdin("verdict_boolean");
  for target in ["x86-64-linux", "arm64-linux", "arm64-darwin", "wasm32-wasi"] {
    let detailed: semlower.Verdict = semlower.verdict(mod, target);
    let ok: boolean = semlower.verdict_ok(mod, target);
    if (ok != detailed.ok) {
      eprint(target + ": verdict mismatch\n" + semlower.verdict_text(detailed));
      return 1;
    }
    if (ok) { write("ir\n"); } else { write("refused\n"); }
  }
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "drivers", "verdict_boolean.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := buildSelfHostBin(t, gcc, dir, "drivers/verdict_boolean.fern", "verdict_boolean")
	for _, c := range irProbeCases() {
		t.Run(c.name, func(t *testing.T) {
			want := "refused\n"
			if c.wantVerdict == "module: IR" {
				want = "ir\n"
			}
			if got := string(runCapture(t, gcc, runner, probe, []byte(c.src))); got != strings.Repeat(want, 4) {
				t.Errorf("target verdicts = %q, want %q", got, strings.Repeat(want, 4))
			}
		})
	}
}

const viewTemplateSrc = "pub function first[T](f: () => T): T {\n let xs: T[] = [f()];\n return xs[0];\n}\n" +
	"function main(): i32 {\n let b: string = \"abcdefgh\";\n print(first((): str => slice_unchecked(b, 2, 5)));\n return 0;\n}\n"

func firstNLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
