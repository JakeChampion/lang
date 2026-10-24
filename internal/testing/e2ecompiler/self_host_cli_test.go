package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCLIX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")

	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	runDriver := func(t *testing.T, args ...string) ([]byte, int) {
		t.Helper()
		if !slices.Contains(args, "-target") {
			args = append([]string{"-target", "x86-64-linux", "-emit", "asm"}, args...)
		}
		cmd := exec.Command(fernBin, args...)
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	t.Run("interp-runs-the-build-gate", func(t *testing.T) {
		path := filepath.Join(dir, "interp_ill_typed.fern")
		if err := os.WriteFile(path, []byte("function main(): i32 { let x: i32 = \"s\"; return 7; }\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		cmd := exec.Command(fernBin, "-interp", path)
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("-interp exit = %d, want 1 (out: %s)", code, out)
		}
		if !strings.Contains(string(out), "error[E003]") {
			t.Errorf("-interp diagnostics = %q, want error[E003]", out)
		}
	})

	t.Run("async-function-through-the-driver", func(t *testing.T) {
		src := "async function work(): i32 { return 7; }\nfunction main(): i32 { return work(); }\n"
		path := filepath.Join(dir, "async_prog.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		out, code := runDriver(t, "-fmt", path)
		if code != 0 {
			t.Fatalf("-fmt exit = %d, want 0 (out: %s)", code, out)
		}
		if !strings.HasPrefix(string(out), "async function work") {
			t.Errorf("-fmt dropped the async modifier; got:\n%s", out)
		}
		if _, code := runDriver(t, "-interp", path); code != 7 {
			t.Errorf("-interp exit = %d, want 7", code)
		}
		checkCmd := exec.Command(fernBin, "-check", path)
		var checkErr bytes.Buffer
		checkCmd.Stderr = &checkErr
		if err := checkCmd.Run(); err != nil {
			t.Errorf("-check failed: %v (stderr: %s)", err, checkErr.String())
		}
		if checkErr.Len() != 0 {
			t.Errorf("-check exited 0 but wrote diagnostics:\n%s", checkErr.String())
		}
		identPath := filepath.Join(dir, "async_ident.fern")
		identSrc := "function main(): i32 { let async: i32 = 3; return async + 4; }\n"
		if err := os.WriteFile(identPath, []byte(identSrc), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, code := runDriver(t, "-interp", identPath); code != 7 {
			t.Errorf("`async` as an identifier: exit = %d, want 7", code)
		}
	})

	t.Run("stdlib-import-without-root", func(t *testing.T) {
		path := filepath.Join(dir, "unrooted_prog.fern")
		src := "import \"std/nosuchmod\";\nfunction main(): i32 { return 0; }\n"
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", path).CombinedOutput()
		if want := `import "std/nosuchmod" needs the stdlib root`; !strings.Contains(string(combined), want) {
			t.Errorf("-check without a root: want %q, got:\n%s", want, combined)
		}
		if _, code := runDriver(t, "-check", path); code == 0 {
			t.Errorf("-check without a root exited 0")
		}
	})

	t.Run("imported-match-guard", func(t *testing.T) {
		proj := t.TempDir()
		writeTree(t, proj, map[string]string{
			"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.classify(Some(50)) * 10 + lib.classify(Some(3)); }\n",
			"lib.fern":  "function big(n: i32): boolean { return n > 10; }\npub function classify(o: Option[i32]): i32 {\n  match (o) {\n    Some(n) when big(n) => { return 2; },\n    Some(_) => { return 1; },\n    None => { return 0; }\n  }\n}\n",
		})
		if _, code := runDriver(t, "-interp", filepath.Join(proj, "main.fern")); code != 21 {
			t.Errorf("-interp exit = %d, want 21", code)
		}
	})

	t.Run("nested-relative-import", func(t *testing.T) {
		proj := t.TempDir()
		files := map[string]string{
			"main.fern":  "import \"./lib/a\";\nimport \"./lib/b\";\nfunction main(): i32 { return a.value() + b.zero(); }\n",
			"lib/a.fern": "import \"./b\";\npub function value(): i32 { return b.value(); }\n",
			"lib/b.fern": "pub function value(): i32 { return 42; }\npub function zero(): i32 { return 0; }\n",
			"b.fern":     "pub function value(): i32 { return 7; }\npub function zero(): i32 { return 0; }\n",
		}
		for name, src := range files {
			p := filepath.Join(proj, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		if _, code := runDriver(t, "-interp", filepath.Join(proj, "main.fern")); code != 42 {
			t.Errorf("-interp exit = %d, want 42: `./b` from lib/a.fern must be lib/b.fern, not the entry's b.fern", code)
		}
	})

	t.Run("wasm-u32-to-string-is-defined", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping u32-to-string wasm check")
		}
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/u32";

function main(): i32 {
    let u: u32 = 3000000000 as u32;
    let s: string = u.to_string();
    if (s == "3000000000") { return 42; }
    return 7;
}
`
		srcPath := filepath.Join(dir, "u32_to_string.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
		}
		for _, name := range undefinedWatCalls(wat) {
			t.Errorf("emitted WAT calls $%s, which it neither defines nor imports (#5992)", name)
		}
		watPath := filepath.Join(dir, "u32_to_string.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != 42 {
			t.Errorf("u32.to_string() on wasm exited %d, want 42\n%s", got, out)
		}
	})

	t.Run("wasm-semantic-capture-write", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping capture-write wasm check")
		}
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/i32";

function main(): i32 {
    let m: i32 = 10;
    let bump = (): i32 => { m = m + 1; return m; };
    print(bump().to_string());
    print(bump().to_string());
    print(m.to_string());
    return 0;
}
`
		srcPath := filepath.Join(dir, "capture_write.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
		}
		watPath := filepath.Join(dir, "capture_write.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != 0 {
			t.Errorf("capture write on wasm exited %d, want 0\n%s", got, out)
		}
		if string(out) != "11\n12\n12\n" {
			t.Errorf("capture write on wasm printed %q, want \"11\\n12\\n12\\n\"", out)
		}
	})

	t.Run("two-traits-one-method-emits-one-symbol", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping duplicate-method-symbol check")
		}
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/i32";

struct P { x: i32 }

trait A { function scale(self: Self): i32; }
trait B { function scale(self: Self): i32; }

@noinline function kept(x: i32): i32 { return x; }

impl A for P { function scale(self: Self): i32 { return kept(self.x); } }
impl B for P { function scale(self: Self): i32 { return kept(100); } }

function twice[T: A](v: T): i32 { return v.scale() + v.scale(); }

function once[T: B](v: T): i32 { return v.scale(); }

function main(): i32 {
    let v: P = P { x: 5 };
    return twice(v) + once(v);
}
`
		srcPath := filepath.Join(dir, "two_traits_one_method.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
		}
		if got := bytes.Count(wat, []byte("(func $P.scale ")); got != 1 {
			t.Errorf("emitted WAT defines $P.scale %d times, want exactly 1", got)
		}
		if got := bytes.Count(wat, []byte("(func $P.B.scale ")); got != 1 {
			t.Errorf("emitted WAT defines $P.B.scale %d times, want exactly 1 (B's provider interposed with its trait)", got)
		}
		watPath := filepath.Join(dir, "two_traits_one_method.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != 110 {
			t.Errorf("twice(P{x:5}) + once(P{x:5}) on wasm exited %d, want 110\n%s", got, out)
		}
	})

	t.Run("cross-module-method-collision-emits-one-symbol", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping cross-module collision check")
		}
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/num";

trait MyAdd { function add(self: Self, o: Self): Self; }
@noinline function kept(x: i32): i32 { return x; }

impl MyAdd for i32 { function add(self: Self, o: Self): Self { return kept(self + o + 1); } }

function sum[T: num.Add](a: T, b: T): T { return a.add(b); }
function mine[T: MyAdd](a: T, b: T): T { return a.add(b); }

function main(): i32 { return sum(2, 3) * 10 + mine(2, 3); }
`
		srcPath := filepath.Join(dir, "cross_module_add.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
		}
		if got := bytes.Count(wat, []byte("(func $i32.add ")); got > 1 {
			t.Errorf("emitted WAT defines $i32.add %d times, want at most 1", got)
		}
		if got := bytes.Count(wat, []byte("(func $i32.__entry.add ")); got != 1 {
			t.Errorf("emitted WAT defines $i32.__entry.add %d times, want exactly 1 (the entry's provider interposed with its namespace)", got)
		}
		watPath := filepath.Join(dir, "cross_module_add.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != 56 {
			t.Errorf("sum(2, 3) * 10 + mine(2, 3) on wasm exited %d, want 56\n%s", got, out)
		}
	})

	t.Run("dyn-picks-the-named-trait-provider", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping dyn provider check")
		}
		const decls = `trait A { function m(self: Self): i32; }
trait B { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
impl B for S { function m(self: Self): i32 { return 7; } }
`
		for _, tc := range []struct {
			name string
			dyn  string
			want int
		}{
			{"dyn-b", "dyn B", 7},
			{"dyn-a", "dyn A", 3},
		} {
			t.Run(tc.name, func(t *testing.T) {
				src := decls + "function main(): i32 {\n    let d: " + tc.dyn + " = S { v: 3 };\n    return d.m();\n}\n"
				srcPath := filepath.Join(dir, "dyn_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
				if code != 0 {
					t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
				}
				watPath := filepath.Join(dir, "dyn_"+tc.name+".wat")
				if err := os.WriteFile(watPath, wat, 0o644); err != nil {
					t.Fatalf("write wat: %v", err)
				}
				cmd := exec.Command("wasmtime", "run", watPath)
				out, _ := cmd.CombinedOutput()
				if got := cmd.ProcessState.ExitCode(); got != tc.want {
					t.Errorf("`%s` dispatch exited %d, want %d\n%s", tc.dyn, got, tc.want, out)
				}
			})
		}
		t.Run("multi-trait-distinct-names", func(t *testing.T) {
			src := `trait A { function m(self: Self): i32; }
trait B { function n(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
impl B for S { function n(self: Self): i32 { return 7; } }
function main(): i32 {
    let d: dyn A + B = S { v: 3 };
    return d.m() + d.n();
}
`
			srcPath := filepath.Join(dir, "dyn_multi.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
			if code != 0 {
				t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
			}
			watPath := filepath.Join(dir, "dyn_multi.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != 10 {
				t.Errorf("`dyn A + B` dispatch exited %d, want 10\n%s", got, out)
			}
		})
	})

	t.Run("emit-stdout", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret42.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, srcPath)
		if code != 0 {
			t.Fatalf("emit exited %d, want 0", code)
		}
		if len(asm) == 0 {
			t.Fatal("emit produced 0 bytes of asm")
		}
		progBin := buildBin(t, gcc, dir, "ret42", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-to-file", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret7.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { let x = 3; let y = 4; return x + y; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		outPath := filepath.Join(dir, "ret7.s")
		stdout, code := runDriver(t, "-o", outPath, srcPath)
		if code != 0 {
			t.Fatalf("`-o` emit exited %d, want 0", code)
		}
		if len(stdout) != 0 {
			t.Errorf("`-o` emit wrote %d bytes to stdout, want 0 (asm goes to the file)", len(stdout))
		}
		asm, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read -o output: %v", err)
		}
		if len(asm) == 0 {
			t.Fatal("`-o` output file is empty")
		}
		progBin := buildBin(t, gcc, dir, "ret7", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 7 {
			t.Errorf("file-emitted program exited %d, want 7", c)
		}
	})

	foldCase := func(t *testing.T, name, src string, wantExit int, unwanted ...string) {
		t.Helper()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defAsm, code := runDriver(t, srcPath)
		if code != 0 {
			t.Fatalf("default emit exited %d, want 0", code)
		}
		for _, u := range unwanted {
			if n := strings.Count(string(defAsm), u); n != 0 {
				t.Errorf("%s: the DEFAULT build left %d %q; constant folding is gated again:\n%s", name, n, u, defAsm)
			}
		}
		optAsm, code := runDriver(t, "-O", srcPath)
		if code != 0 {
			t.Fatalf("-O emit exited %d, want 0", code)
		}
		if string(optAsm) != string(defAsm) {
			t.Errorf("%s: -O changed the emitted code; folding is meant to run on every build\n"+
				"--- -O ---\n%s\n--- default ---\n%s", name, optAsm, defAsm)
		}
		progBin := buildBin(t, gcc, dir, name, string(defAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != wantExit {
			t.Errorf("%s exited %d, want %d", name, c, wantExit)
		}
	}

	t.Run("fold-constants", func(t *testing.T) {
		foldCase(t, "fold", "function main(): i32 { return 2 * 3 + 1; }\n", 7, "imulq", "addq %rcx, %rax")
	})

	t.Run("fold-literal-concat", func(t *testing.T) {
		foldCase(t, "foldconcat",
			"function main(): i32 { let s: string = \"ab\" + \"cd\"; return s.len(); }\n",
			4, "call __fn___fern_str_concat")
	})

	t.Run("fold-guard-concat", func(t *testing.T) {
		foldCase(t, "foldguard", "enum E { A(string) }\n"+
			"function main(): i32 {\n"+
			"    let e: E = E.A(\"abcd\");\n"+
			"    match (e) {\n"+
			"        E.A(s) when s == (\"ab\" + \"cd\") => { return 7; },\n"+
			"        _ => { return 9; },\n"+
			"    }\n"+
			"    return 0;\n"+
			"}\n", 7, "call __fn___fern_str_concat")
	})

	t.Run("fold-i32-min", func(t *testing.T) {
		foldCase(t, "foldmin", "function f(): i32 { return 0 - 2147483647 - 1; }\n"+
			"function main(): i32 { if (f() < 0 - 2147483647) { return 11; } return 1; }\n", 11)
	})

	t.Run("fold-hex-constants", func(t *testing.T) {
		foldCase(t, "foldhex", "function main(): i32 { return 0x10 + 1; }\n", 17)
	})

	assertCase := func(t *testing.T, name, src string, wantDefault, wantOpt int) {
		t.Helper()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, leg := range []struct {
			label    string
			args     []string
			wantExit int
			wantMsg  int
		}{
			{"default", nil, wantDefault, 1},
			{"-O", []string{"-O"}, wantOpt, 0},
		} {
			asm, code := runDriver(t, append(append([]string{}, leg.args...), srcPath)...)
			if code != 0 {
				t.Fatalf("%s: %s emit exited %d, want 0", name, leg.label, code)
			}
			if n := strings.Count(string(asm), "assertion failed"); (n > 0) != (leg.wantMsg > 0) {
				t.Errorf("%s: %s build has %d \"assertion failed\" string(s), want %s:\n%s",
					name, leg.label, n, map[bool]string{true: "at least one", false: "none"}[leg.wantMsg > 0], asm)
			}
			progBin := buildBin(t, gcc, dir, name+leg.label, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if c := cmd.ProcessState.ExitCode(); c != leg.wantExit {
				t.Errorf("%s: %s build exited %d, want %d", name, leg.label, c, leg.wantExit)
			}
		}
	}

	t.Run("opt-elides-assert", func(t *testing.T) {
		assertCase(t, "elide", "function main(): i32 {\n"+
			"    let n: i32 = 3;\n"+
			"    assert(n > 100, \"boom\");\n"+
			"    return 5;\n"+
			"}\n", 1, 5)
	})

	t.Run("opt-elides-nested-asserts", func(t *testing.T) {
		assertCase(t, "elidenest", "enum E { A(i32) }\n"+
			"function run(f: () => i32): i32 { return f(); }\n"+
			"function main(): i32 {\n"+
			"    let n: i32 = 3;\n"+
			"    if (n > 0) { assert(n > 100, \"nested-if\"); }\n"+
			"    let g: i32 = 0;\n"+
			"    while (g < 1) { assert(n > 100, \"while\"); g = g + 1; }\n"+
			"    let r: i32 = run((): i32 => { assert(n > 100, \"lambda\"); return 1; });\n"+
			"    let e: E = E.A(1);\n"+
			"    match (e) {\n"+
			"        E.A(v) => { assert(v > 100, \"match-arm\"); },\n"+
			"        _ => { return 90; }\n"+
			"    }\n"+
			"    let r2: i32 = run((): i32 => {\n"+
			"        return run((): i32 => { assert(n > 100, \"nested-lambda\"); return 2; });\n"+
			"    });\n"+
			"    return r + r2 + 2;\n"+
			"}\n", 1, 5)
	})

	t.Run("opt-elides-assert-in-generic", func(t *testing.T) {
		assertCase(t, "elidegen", "function check_it[T](v: T, n: i32): i32 {\n"+
			"    assert(n > 100, \"generic\");\n"+
			"    return 1;\n"+
			"}\n"+
			"function main(): i32 {\n"+
			"    return check_it(7, 3) + check_it(\"s\", 3) + 3;\n"+
			"}\n", 1, 5)
	})

	t.Run("opt-elides-after-the-gates", func(t *testing.T) {
		srcPath := filepath.Join(dir, "capassert.fern")
		src := "function main(): i32 {\n    assert(proc_fork() >= 0);\n    return 0;\n}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, leg := range [][]string{
			{"-target", "wasm32-wasi", "-emit", "asm", srcPath},
			{"-target", "wasm32-wasi", "-emit", "asm", "-O", srcPath},
		} {
			out, code := runDriver(t, leg...)
			if code == 0 {
				t.Errorf("%v: a capability-violating assert compiled; the elision is running before the gates:\n%s", leg, out)
			}
		}
	})

	t.Run("opt-does-not-widen-what-compiles", func(t *testing.T) {
		for _, tc := range []struct{ name, src, code string }{
			{"undefined-name", "function main(): i32 {\n    assert(nosuchname > 1);\n    return 0;\n}\n", "E001"},
			{"unknown-field", "struct P { x: i32 }\nfunction main(): i32 {\n    let p: P = P { x: 1 };\n    assert(p.nofield > 0);\n    return 0;\n}\n", "E043"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "illassert_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				outDef, codeDef := runDriver(t, srcPath)
				outOpt, codeOpt := runDriver(t, "-O", srcPath)
				if codeDef != codeOpt {
					t.Errorf("default exited %d but -O exited %d; the elision is running before the checker\ndefault:\n%s\n-O:\n%s",
						codeDef, codeOpt, outDef, outOpt)
				}
				if codeDef == 0 {
					t.Errorf("an ill-typed assert compiled on the default build (exit 0); native rejects it")
				}
				diag, _ := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", "-O", srcPath).CombinedOutput()
				if !strings.Contains(string(diag), tc.code) {
					t.Errorf("-O did not reject the assert with the checker's %s:\n%s", tc.code, diag)
				}
			})
		}
	})

	t.Run("opt-still-elides-a-well-typed-assert", func(t *testing.T) {
		assertCase(t, "elidecontrol", "function main(): i32 {\n"+
			"    let n: i32 = 3;\n"+
			"    assert(n > 100, \"control\");\n"+
			"    return 5;\n"+
			"}\n", 1, 5)
	})

	t.Run("check-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ok.fern")
		if err := os.WriteFile(srcPath, []byte("function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-check", srcPath)
		if code != 0 {
			t.Errorf("-check on well-typed program exited %d, want 0", code)
		}
	})

	t.Run("check-accepts-typed-field", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  string
		}{
			{"untyped-literal-at-i64-field", "struct W { a: i32, b: i64 }\nfunction mk(n: i32): W { return W { a: n, b: 0 }; }\nfunction main(): i32 { return mk(1).a; }\n"},
			{"member-widens-to-union-field", "struct Lf { v: i32 }\nstruct Tw { xs: i32[] }\ntype Node = Lf | Tw;\nstruct Holds { t: Node, n: i32 }\nfunction mk(n: i32): Holds { return Holds { t: Lf { v: n }, n: n }; }\nfunction main(): i32 { return mk(1).n; }\n"},
			{"member-widens-in-struct-update", "struct Lf { v: i32 }\nstruct Tw { xs: i32[] }\ntype Node = Lf | Tw;\nstruct Holds { t: Node, n: i32 }\nfunction upd(h: Holds, n: i32): Holds { return Holds { ...h, t: Lf { v: n } }; }\nfunction main(): i32 { return upd(Holds { t: Lf { v: 1 }, n: 2 }, 3).n; }\n"},
			{"same-value-via-local", "struct Lf { v: i32 }\nstruct Tw { xs: i32[] }\ntype Node = Lf | Tw;\nstruct Holds { t: Node, n: i32 }\nfunction mk(n: i32): Holds { let t: Node = Lf { v: n }; return Holds { t: t, n: n }; }\nfunction main(): i32 { return mk(1).n; }\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "typed_field.fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				out, code := runDriver(t, "-check", srcPath)
				if code != 0 {
					t.Errorf("-check exited %d on a program native accepts, want 0:\n%s", code, out)
				}
			})
		}
	})

	t.Run("check-accepts-derived-assoc-call", func(t *testing.T) {
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		srcPath := filepath.Join(dir, "derived_assoc.fern")
		src := "import \"std/json\";\n@derive(json.FromJson) struct User { id: i32 }\nfunction main(): i32 { let r: Result[User, string] = User.from_json(\"{\\\"id\\\":7}\"); match (r) { Ok(u) => { return u.id; }, Err(e) => { return 1; } } }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out, code := runDriver(t, "-check", srcPath, stdlibRoot)
		if code != 0 {
			t.Errorf("-check exited %d on a program native accepts, want 0:\n%s", code, out)
		}
	})

	t.Run("check-refuses-use-with-unbound-callback-parameter", func(t *testing.T) {
		srcPath := filepath.Join(dir, "use_unbound.fern")
		src := "function apply[T, I](it: I, cb: (T) => i32): i32 { return 0; }\nfunction main(): i32 {\n    use n <- apply(5);\n    return 1;\n}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out, err := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "E032") || !strings.Contains(string(out), "E038") {
			t.Errorf("-check (err %v) did not report E032 and E038, the refusals native reports:\n%s", err, out)
		}
	})

	t.Run("check-warns-about-todo-stubs", func(t *testing.T) {
		sub := filepath.Join(dir, "todo_check")
		writeTree(t, sub, map[string]string{
			"main.fern": "import \"./lib\";\nfunction f(): i32 {\n  todo;\n}\nfunction g(): i32 { todo(\"later\"); }\nfunction main(): i32 {\n  return lib.h();\n}\n",
			"lib.fern":  "pub function h(): i32 {\n  return 0;\n}\nfunction unfinished(): i32 {\n  todo;\n}\n",
		})
		src, err := os.ReadFile(filepath.Join(sub, "main.fern"))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ arg, name string }{{"main.fern", "main.fern"}, {"-", "<stdin>"}} {
			cmd := exec.Command(fernBin, "-check", c.arg)
			cmd.Dir = sub
			cmd.Stdin = bytes.NewReader(src)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Errorf("-check %s: %v (stderr: %s)", c.arg, err, stderr.String())
			}
			want := c.name + ":3:3: warning: `todo` stub remaining\n" + c.name + ":5:21: warning: `todo` stub remaining\n"
			if c.arg != "-" {
				want = c.name + ":3:3: warning: `todo` stub remaining\n      todo;\n      ^\n" +
					c.name + ":5:21: warning: `todo` stub remaining\n    function g(): i32 { todo(\"later\"); }\n                        ^\n"
			}
			if stderr.String() != want {
				t.Errorf("-check %s wrote %q, want %q", c.arg, stderr.String(), want)
			}
		}
	})

	t.Run("check-parse-unknown-gate", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  string
		}{
			{"body-sentinel", "function main(): i32 { let x: i32 = ;\n  return 0; }\n"},
			{"param-default-sentinel", "function f(x: i32 = ;): i32 { return x; }\nfunction main(): i32 { return 0; }\n"},
			{"param-default-at", "function f(x: i32 = @): i32 { return x; }\nfunction main(): i32 { return 0; }\n"},
			{"param-default-float-range", "function f(x: f64 = 1e309): f64 { return x; }\nfunction main(): i32 { return 0; }\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "parse_unknown.fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
				got := uniqueSortedCodes(frontEndCodeRE.FindAllString(string(combined), -1))
				want := goFrontEndCodes(t, tc.src)
				if !equalStrings(got, want) {
					t.Errorf("-check codes = %v, want native's %v\nself-host output:\n%s", got, want, combined)
				}
				if _, code := runDriver(t, "-check", srcPath); code != 1 {
					t.Errorf("-check exited %d, want 1", code)
				}
			})
		}
		cleanPath := filepath.Join(dir, "parse_unknown_clean.fern")
		if err := os.WriteFile(cleanPath, []byte("function f(x: i32 = 41): i32 { return x; }\nfunction main(): i32 { return f(); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if out, code := runDriver(t, "-check", cleanPath); code != 0 {
			t.Errorf("-check on a readable default exited %d, want 0:\n%s", code, out)
		}
	})

	t.Run("check-top-level-stmt-gate", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  string
		}{
			{"let", "let g: i32 = 1;\nfunction main(): i32 { return 0; }\n"},
			{"call", "print(\"hi\");\nfunction main(): i32 { return 0; }\n"},
			{"if", "if (1 > 0) { }\nfunction main(): i32 { return 0; }\n"},
			{"lambda", "(): i32 => {\n  return 1;\n}\nfunction main(): i32 { return 0; }\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "toplevel.fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
				got := uniqueSortedCodes(frontEndCodeRE.FindAllString(string(combined), -1))
				want := goFrontEndCodes(t, tc.src)
				if !equalStrings(got, want) {
					t.Errorf("-check codes = %v, want native's %v\nself-host output:\n%s", got, want, combined)
				}
				if _, code := runDriver(t, "-check", srcPath); code != 1 {
					t.Errorf("-check exited %d, want 1 — the statement is dead code", code)
				}
			})
		}
		scriptPath := filepath.Join(dir, "script.fern")
		if err := os.WriteFile(scriptPath, []byte("let x = 1 + 2 * 3;\nreturn x;\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if out, code := runDriver(t, "-interp", scriptPath); code != 7 {
			t.Errorf("-interp on a no-main script exited %d, want 7 (the top-level return):\n%s", code, out)
		}
	})

	t.Run("check-str-view-arg-is-a-borrow", func(t *testing.T) {
		for _, src := range []string{
			"function g(x: string): i32 { return x.len(); }\nfunction main(): i32 { let t: string = \"abcdef\"; return g(slice_unchecked(t, 0, 3)); }\n",
			"function g(x: string): i32 { return x.len(); }\nfunction main(): i32 { let t: string = \"abcdef\"; let v: str = slice_unchecked(t, 0, 3); return g(v); }\n",
			"function g(x: str): i32 { return x.len(); }\nfunction main(): i32 { let t: string = \"abcdef\"; return g(slice_unchecked(t, 0, 3)); }\n",
		} {
			srcPath := filepath.Join(dir, "strview_arg.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out, code := runDriver(t, "-check", srcPath)
			if code != 0 {
				t.Errorf("-check exited %d, want 0 — a view lent to a parameter is a borrow:\n%s\nsource:\n%s", code, out, src)
			}
		}
		srcPath := filepath.Join(dir, "strview_owned.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { let t: string = \"abcdef\"; let s: string = slice_unchecked(t, 0, 3); return s.len(); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 1 {
			t.Errorf("-check on a view stored into an owned string exited %d, want 1", code)
		}
		ownPath := filepath.Join(dir, "strview_own_param.fern")
		if err := os.WriteFile(ownPath, []byte("function g(own x: string): i32 { return x.len(); }\nfunction main(): i32 { let t: string = \"abcdef\"; return g(slice_unchecked(t, 0, 3)); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out, code := runDriver(t, "-check", ownPath)
		if code != 1 {
			t.Errorf("-check on a view passed to an `own` parameter exited %d, want 1:\n%s", code, out)
		}
		combined, _ := exec.Command(fernBin, "-check", ownPath).CombinedOutput()
		if !strings.Contains(string(combined), "error[E051]") {
			t.Errorf("-check on a view passed to an `own` parameter = %q, want error[E051]", combined)
		}
	})

	t.Run("check-bad", func(t *testing.T) {
		srcPath := filepath.Join(dir, "bad.fern")
		if err := os.WriteFile(srcPath, []byte("function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(1); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-check", srcPath)
		if code != 1 {
			t.Errorf("-check on ill-typed program exited %d, want 1", code)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "error[E004]") {
			t.Errorf("-check diagnostics = %q, want it to contain error[E004]", combined)
		}
	})

	t.Run("payloadless-variant-as-payload-binder", func(t *testing.T) {
		decls := "enum Res2 { Ok2(i32), Er2 }\nenum Nest3 { Wrap(Res2), Bare }\n"
		bad := decls + "function g(w: Nest3): i32 { match (w) { Wrap(Er2) => { return 9; }, _ => { return 1; } } }\nfunction main(): i32 { return g(Nest3.Wrap(Res2.Ok2(5))); }\n"
		badPath := filepath.Join(dir, "plbinder_bad.fern")
		if err := os.WriteFile(badPath, []byte(bad), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", badPath); code != 1 {
			t.Errorf("self-host -check on a payload-less-variant binder exited %d, want 1", code)
		}
		combined, _ := exec.Command(fernBin, "-check", badPath).CombinedOutput()
		if !strings.Contains(string(combined), "error[E015]") {
			t.Errorf("self-host -check diagnostics = %q, want error[E015]", combined)
		}
		if !strings.Contains(string(combined), "Er2()") {
			t.Errorf("diagnostic must name the spelling that works: %q", combined)
		}

		ok := decls + "enum Other { Zed, Yy(i32) }\nfunction g(w: Nest3): i32 { match (w) { Wrap(x) => { return 1; }, Bare => { return 2; } } }\nfunction h(w: Nest3): i32 { match (w) { Wrap(Zed) => { return 1; }, Bare => { return 2; } } }\nfunction main(): i32 { return g(Nest3.Bare) + h(Nest3.Bare); }\n"
		okPath := filepath.Join(dir, "plbinder_ok.fern")
		if err := os.WriteFile(okPath, []byte(ok), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		okOut, _ := exec.Command(fernBin, "-check", okPath).CombinedOutput()
		if strings.Contains(string(okOut), "error[E015]") {
			t.Errorf("rule fired on a legitimate binder: %q", okOut)
		}
	})

	t.Run("float-literal-out-of-range", func(t *testing.T) {
		srcPath := filepath.Join(dir, "float_oor.fern")
		src := "function main(): i32 { let x: f64 = 1e309; if (x > 1.0e308) { return 7; } return 0; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, mode := range [][]string{{"-check"}, {"-interp"}, {"-target", "x86-64-linux", "-emit", "asm"}} {
			cmd := exec.Command(fernBin, append(append([]string{}, mode...), srcPath)...)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code == 0 {
				t.Errorf("%v: exited 0, want a rejection (out: %s)", mode, out)
			}
			if !strings.Contains(string(out), "error[P002]") {
				t.Errorf("%v: diagnostics = %q, want error[P002]", mode, out)
			}
		}
		fmtOut, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Errorf("-fmt exited %d, want 0 (out: %s)", code, fmtOut)
		}
		if !strings.Contains(string(fmtOut), "1e309") {
			t.Errorf("-fmt lost the literal; got:\n%s", fmtOut)
		}
		okPath := filepath.Join(dir, "float_underflow.fern")
		okSrc := "function main(): i32 { let x: f64 = 1e-400; if (x == 0.0) { return 7; } return 0; }\n"
		if err := os.WriteFile(okPath, []byte(okSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", okPath); code != 0 {
			t.Errorf("-check on an underflowing literal exited %d, want 0", code)
		}
		asm, code := runDriver(t, okPath)
		if code != 0 {
			t.Fatalf("emit on an underflowing literal exited %d, want 0", code)
		}
		progBin := buildBin(t, gcc, dir, "float_underflow", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 7 {
			t.Errorf("underflowing-literal program exited %d, want 7", c)
		}
	})

	t.Run("check-enum-value-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "enum_value.fern")
		src := "enum Color { Red, Green }\n" +
			"function pick(): Color { return Green; }\n" +
			"function main(): i32 { let c: Color = Red; let d: Color = pick(); return 0; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on an enum-value program exited %d, want 0", code)
		}
	})

	t.Run("check-option-result-value-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "option_result_value.fern")
		src := "function main(): i32 {\n" +
			"    let a: Option[i32] = Some(3);\n" +
			"    let b: Option[i32] = None;\n" +
			"    let c: Result[i32, i32] = Ok(3);\n" +
			"    let d: Result[i32, i32] = Err(9);\n" +
			"    return 0;\n" +
			"}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on an Option/Result-value program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-body-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "generic_body.fern")
		if err := os.WriteFile(srcPath, []byte("function ident[T](v: T): T { return v; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a declared-generic program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-call-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "generic_call.fern")
		if err := os.WriteFile(srcPath, []byte("function ident[T](v: T): T { return v; }\nfunction main(): i32 { return ident(3); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a generic-call program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-struct-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "generic_struct.fern")
		if err := os.WriteFile(srcPath, []byte("struct Box[T] { v: T }\nfunction main(): i32 { let b: Box[i32] = Box { v: 3 }; return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a generic-struct program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-struct-field-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "generic_struct_field.fern")
		if err := os.WriteFile(srcPath, []byte("struct Box[T] { v: T }\nfunction main(): i32 { let b: Box[i32] = Box { v: 3 }; return b.v; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a generic-struct field-access program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-nested-field-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "generic_nested_field.fern")
		src := "struct Wrapper[T] { items: T[] }\n" +
			"struct Pair[K, V] { kv: (K, V) }\n" +
			"function main(): i32 {\n" +
			"    let w: Wrapper[i32] = Wrapper { items: [1, 2, 3] };\n" +
			"    let p: Pair[i32, string] = Pair { kv: (7, \"hi\") };\n" +
			"    return w.items[0] + p.kv.0;\n" +
			"}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a nested generic field-access program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-method-ret-ok", func(t *testing.T) {
		bare := filepath.Join(dir, "generic_method.fern")
		if err := os.WriteFile(bare, []byte("struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { let b: Box[i32] = Box { v: 5 }; return b.get(); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", bare); code != 0 {
			t.Errorf("-check on a generic-method-return program exited %d, want 0", code)
		}
		nested := filepath.Join(dir, "generic_method_nested.fern")
		if err := os.WriteFile(nested, []byte("struct Wrapper[T] { items: T[] }\nfunction (w: Wrapper[T]) all(): T[] { return w.items; }\nfunction main(): i32 { let w: Wrapper[i32] = Wrapper { items: [1, 2] }; return w.all()[0]; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", nested); code != 0 {
			t.Errorf("-check on a nested generic-method-return program exited %d, want 0", code)
		}
	})

	t.Run("check-dyn-bind-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "dyn_bind.fern")
		src := "trait Greet { function hi(self: Self): i32; }\n" +
			"struct Dog { }\n" +
			"impl Greet for Dog { function hi(self: Self): i32 { return 7; } }\n" +
			"function main(): i32 { let d: dyn Greet = Dog { }; return 0; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a dyn-binding program exited %d, want 0", code)
		}
	})

	t.Run("check-dyn-dispatch-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "dyn_dispatch.fern")
		src := "trait Greet { function hi(self: Self): i32; }\n" +
			"struct Dog { }\n" +
			"impl Greet for Dog { function hi(self: Self): i32 { return 7; } }\n" +
			"function main(): i32 { let d: dyn Greet = Dog { }; return d.hi(); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if out, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a dyn-dispatch program exited %d, want 0\n%s", code, out)
		}
	})

	t.Run("check-selfhost-no-e001", func(t *testing.T) {
		for _, m := range []string{"asmcore.fern", "lexer.fern", "parser.fern", "checker.fern", "flatten.fern", "interp.fern", "printer.fern", "astwalk.fern", "ssa.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "arm64_native.fern", "x86_native.fern", "elf.fern", "fern.fern"} {
			combined, _ := exec.Command(fernBin, "-check", filepath.Join(dir, m)).CombinedOutput()
			if strings.Contains(string(combined), "error[E001]") {
				t.Errorf("-check on self-host module %s reported a spurious E001:\n%s", m, combined)
			}
		}
	})

	t.Run("check-position-undefined-assign", func(t *testing.T) {
		srcPath := filepath.Join(dir, "undef_assign.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { y = 5; return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "1:24: error[E001]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"1:24: error[E001]\"", combined)
		}
	})

	t.Run("check-position", func(t *testing.T) {
		srcPath := filepath.Join(dir, "enum_redecl.fern")
		if err := os.WriteFile(srcPath, []byte("enum Opt { A, B }\nenum Opt { C, D }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "2:1: error[E006]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"2:1: error[E006]\"", combined)
		}
	})

	t.Run("check-position-array-literal", func(t *testing.T) {
		srcPath := filepath.Join(dir, "fip_array.fern")
		src := "fip function f(n: i32): i32 {\n    let a: i32[] = [1];\n    return n + a.len();\n}\nfunction main(): i32 { return f(1); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "2:20: error[E053]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"2:20: error[E053]\"", combined)
		}
	})

	t.Run("check-position-struct", func(t *testing.T) {
		srcPath := filepath.Join(dir, "dup_field.fern")
		if err := os.WriteFile(srcPath, []byte("struct P { x: i32, x: i32 }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "1:1: error[E007]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"1:1: error[E007]\"", combined)
		}
	})

	t.Run("check-position-func", func(t *testing.T) {
		dupParam := filepath.Join(dir, "dup_param.fern")
		if err := os.WriteFile(dupParam, []byte("function f(a: i32, a: i32): i32 { return a; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, _ := exec.Command(fernBin, "-check", dupParam).CombinedOutput()
		if !strings.Contains(string(out1), "1:1: error[E018]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"1:1: error[E018]\"", out1)
		}
		redecl := filepath.Join(dir, "func_redecl.fern")
		if err := os.WriteFile(redecl, []byte("function f(): i32 { return 1; }\nfunction f(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out2, _ := exec.Command(fernBin, "-check", redecl).CombinedOutput()
		if !strings.Contains(string(out2), "2:1: error[E006]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"2:1: error[E006]\"", out2)
		}
	})

	t.Run("check-position-var", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"dup_var", "function main(): i32 { let x: i32 = 1; let x: i32 = 2; return x; }\n", "1:40: error[E013]"},
			{"var_mismatch", "function main(): i32 { let x: i32 = \"no\"; return x; }\n", "1:24: error[E003]"},
			{"empty_array", "function main(): i32 { let z = []; return 0; }\n", "1:24: error[E020]"},
			{"assign_mismatch", "function main(): i32 { let x: i32 = 1; x = \"no\"; return x; }\n", "1:42: error[E003]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-return", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"ret_mismatch", "function main(): i32 { let s: string = \"x\"; return s; }\n", "1:45: error[E002]"},
			{"ret_no_value", "function f(): i32 { return; }\nfunction main(): i32 { return 0; }\n", "1:21: error[E012]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-tuple-destructure", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"non_tuple", "function main(): i32 { let n = 5; let (a, b) = n; return a + b; }\n", "1:35: error[E024]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-bad-receiver", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"unknown_receiver", "function (r: Nope) m(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", "1:1: error[E021]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-missing-return", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"falls_off_end", "function f(): i32 { let x = 1; }\nfunction main(): i32 { return 0; }\n", "1:1: error[E052]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-union-collision", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"name_collision", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type B = A | C;\nfunction main(): i32 { return 0; }\n", "4:5: error[E016]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-qualifier-mismatch", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"wrong_qualifier", "enum E { A, B }\nenum F { C, D }\nfunction f(e: E): i32 { match (e) { F.A => { return 1; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A); }\n", "3:37: error[E029]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-foreign-variant", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"foreign_variant", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, C(c) => { return c.z; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", "5:62: error[E014]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-exhaustiveness", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"missing_variant", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", "4:25: error[E030]"},
			{"enum_missing_variant", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { A => { return 1; } } return 0; }\nfunction main(): i32 { return f(A); }\n", "2:25: error[E030]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-struct-field-type", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"string_for_i32", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1, y: \"no\" }; return 0; }\n", "2:48: error[E043]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-method-argtype", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"string_for_i32", "struct P { x: i32 }\nfunction (p: P) add(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { let p: P = P { x: 1 }; let s: string = \"n\"; return p.add(s); }\n", "3:81: error[E038]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-method-arity", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"method_too_few", "struct P { x: i32 }\nfunction (p: P) add(a: i32, b: i32): i32 { return p.x + a + b; }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.add(5); }\n", "3:59: error[E004]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-match-nonenum", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"variant_on_i32", "enum E { A, B }\nfunction main(): i32 { let n: i32 = 5; match (n) { A => { return 1; }, _ => { return 0; } } }\n", "2:52: error[E035]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-array-elem", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"string_in_i32", "function main(): i32 { let a = [1, \"x\", 3]; return 0; }\n", "1:36: error[E034]"},
			{"i32_in_string", "function main(): i32 { let a = [\"a\", 1]; return 0; }\n", "1:38: error[E034]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-type-arity", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"too_many_args", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", "1:1: error[E019]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-match-arm", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"wildcard_not_last", "enum E { A, B }\nfunction main(): i32 { let e: E = E.A; match (e) { _ => { return 0; }, A => { return 1; } } }\n", "2:52: error[E026]"},
			{"dup_variant", "enum E { A, B }\nfunction main(): i32 { let e: E = E.A; match (e) { A => { return 0; }, A => { return 1; }, B => { return 2; } } }\n", "2:72: error[E028]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-break-continue", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"break", "function main(): i32 { break; return 0; }\n", "1:24: error[E011]"},
			{"continue", "function main(): i32 { continue; return 0; }\n", "1:24: error[E011]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-cond-slice", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"if_cond", "function main(): i32 { if (5) { return 1; } return 0; }\n", "1:28: error[E008]"},
			{"while_cond", "function main(): i32 { while (5) { return 1; } return 0; }\n", "1:31: error[E008]"},
			{"slice_low", "function main(): i32 { let a = [1,2,3]; let s: string = \"x\"; let b = a[s:2]; return 0; }\n", "1:72: error[E037]"},
			{"slice_high", "function main(): i32 { let a = [1,2,3]; let s: string = \"x\"; let b = a[0:s]; return 0; }\n", "1:74: error[E037]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-cast", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"bool_to_i32", "function main(): i32 { let b: boolean = true; return b as i32; }\n", "1:56: error[E033]"},
			{"i32_to_bool", "function main(): i32 { let x: i32 = 1; let b: boolean = x as boolean; return 0; }\n", "1:59: error[E033]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-field-assign", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"field_assign", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x = 5; return p.x; }\n", "2:48: error[E048]"},
			{"field_compound", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x += 5; return p.x; }\n", "2:48: error[E048]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("compile-rejects-immutable-mutation", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			src    string
			reject string
		}{
			{"field-assign", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x = 9; return p.x; }\n", "error[E048]"},
			{"field-compound", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p.x += 5; return p.x; }\n", "error[E048]"},
			{"subscript-assign", "function main(): i32 { let a: i32[] = [1, 2, 3]; a[0] = 9; return a[0]; }\n", "error[E056]"},
			{"functional-update-ok", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; p = P { ...p, x: 9 }; return p.x; }\n", ""},
			{"with-ok", "function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.with(0, 9); return a[0]; }\n", ""},
		} {
			sp := filepath.Join(dir, "compile_"+c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			cmd := exec.Command(fernBin, sp)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			code := cmd.ProcessState.ExitCode()
			if c.reject == "" {
				if code != 0 || stdout.Len() == 0 {
					t.Errorf("%s: valid program rejected (exit %d, %d bytes asm)\nstderr: %s", c.name, code, stdout.Len(), stderr.String())
				}
				continue
			}
			if code == 0 {
				t.Errorf("%s: forbidden mutation compiled (exit 0, %d bytes asm) — should be rejected", c.name, stdout.Len())
			}
			if !strings.Contains(stderr.String(), c.reject) {
				t.Errorf("%s: stderr = %q, want %q", c.name, stderr.String(), c.reject)
			}
		}
	})

	t.Run("check-position-field", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"no_field", "struct P { x: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.y; }\n", "2:55: error[E043]"},
			{"bad_tuple_idx", "function main(): i32 { let t = (1, 2); return t.foo; }\n", "1:48: error[E046]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-operator", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"and_nonbool", "function main(): i32 { let b: boolean = true; let x: boolean = b && 5; return 0; }\n", "1:66: error[E009]"},
			{"not_nonbool", "function main(): i32 { let x: boolean = !5; return 0; }\n", "1:41: error[E009]"},
			{"compare_mismatch", "function main(): i32 { let t = (1 == \"x\"); return 0; }\n", "1:35: error[E041]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-ident", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"ambiguous_variant", "enum A { Foo, Bar }\nenum B { Foo, Baz }\nfunction main(): i32 { let x = Foo; return 0; }\n", "3:32: error[E036]"},
			{"arg_ident", "function f(a: string): i32 { return 0; }\nfunction main(): i32 { let n: i32 = 5; return f(n); }\n", "2:49: error[E038]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-number", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"lit_overflow", "function main(): i32 { let x: i32 = 9999999999; return x; }\n", "1:37: error[E047]"},
			{"arg_number", "function f(a: string): i32 { return 0; }\nfunction main(): i32 { return f(5); }\n", "2:33: error[E038]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-call", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"call_arity", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return f(1); }\n", "2:32: error[E004]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-structlit", func(t *testing.T) {
		for _, c := range []struct{ name, src, want string }{
			{"missing_field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p: P = P { x: 1 }; return p.x; }\n", "2:35: error[E005]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("interp", func(t *testing.T) {
		srcPath := filepath.Join(dir, "interp_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { let x = 5; let y = 8; return x * y - 1; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-interp", srcPath)
		if code != 39 {
			t.Errorf("-interp program exited %d, want 39", code)
		}
	})

	t.Run("fmt", func(t *testing.T) {
		srcPath := filepath.Join(dir, "messy.fern")
		messy := "function   main( ):i32{let x=1;let y=2;return x+y;}\n"
		if err := os.WriteFile(srcPath, []byte(messy), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		if len(out1) == 0 {
			t.Fatal("-fmt produced empty output")
		}
		src2Path := filepath.Join(dir, "formatted.fern")
		if err := os.WriteFile(src2Path, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", src2Path)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
		_, checkCode := runDriver(t, "-check", src2Path)
		if checkCode != 0 {
			t.Errorf("formatted output failed -check (exit %d)", checkCode)
		}
	})

	t.Run("fmt-enum", func(t *testing.T) {
		srcPath := filepath.Join(dir, "enum_fmt.fern")
		src := "@must_consume\nenum Shape[T] { Circle(i32), Blank }\n" +
			"function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Blank => { return 0; } } return 0; }\n" +
			"function main(): i32 { return area(Circle(3)); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		got := string(out1)
		if !strings.Contains(got, "@must_consume\nenum Shape[T] { Circle(i32), Blank }") {
			t.Errorf("-fmt did not reconstruct the enum:\n%s", got)
		}
		if strings.Contains(got, "struct Circle") || strings.Contains(got, "struct Blank") {
			t.Errorf("-fmt emitted a variant as a bare struct:\n%s", got)
		}
		fmtPath := filepath.Join(dir, "enum_fmt_out.fern")
		if err := os.WriteFile(fmtPath, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", fmtPath)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent on an enum:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
		if _, checkCode := runDriver(t, "-check", fmtPath); checkCode != 0 {
			t.Errorf("formatted output failed -check (exit %d)", checkCode)
		}
	})

	t.Run("fmt-const", func(t *testing.T) {
		srcPath := filepath.Join(dir, "const_fmt.fern")
		src := "const LIMIT: i32 = 41;\nfunction main(): i32 { return LIMIT + 1; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		if !strings.Contains(string(out1), "const LIMIT: i32 = 41;") {
			t.Errorf("-fmt did not reconstruct the const:\n%s", out1)
		}
		if strings.Contains(string(out1), "function LIMIT") {
			t.Errorf("-fmt emitted the const as its desugared function:\n%s", out1)
		}
		fmtPath := filepath.Join(dir, "const_fmt_out.fern")
		if err := os.WriteFile(fmtPath, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", fmtPath)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent on a const:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
	})

	t.Run("emit-target-arm64", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		srcPath := filepath.Join(dir, "arm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		progBin := filepath.Join(dir, "arm_prog.bin")
		_, code := runDriver(t, "-target", "arm64-linux", "-o", progBin, srcPath)
		if code != 0 {
			t.Fatalf("-target arm64-linux emit exited %d, want 0", code)
		}
		raw, err := os.ReadFile(progBin)
		if err != nil {
			t.Fatalf("read emitted binary: %v", err)
		}
		f, err := elf.NewFile(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("-target arm64-linux output is not a parseable ELF: %v", err)
		}
		if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
			t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
		}
		if err := os.Chmod(progBin, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		cmd := runArm64Bin(qemu, progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("arm64-emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-target-arm64-android", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		srcPath := filepath.Join(dir, "android_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"droid\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		progBin := filepath.Join(dir, "android_prog.bin")
		_, code := runDriver(t, "-target", "arm64-android", "-o", progBin, srcPath)
		if code != 0 {
			t.Fatalf("-target arm64-android emit exited %d, want 0", code)
		}
		raw, err := os.ReadFile(progBin)
		if err != nil {
			t.Fatalf("read emitted binary: %v", err)
		}
		f, err := elf.NewFile(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("-target arm64-android output is not a parseable ELF: %v", err)
		}
		if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_DYN {
			t.Fatalf("got machine=%v type=%v, want AARCH64/DYN", f.Machine, f.Type)
		}
		if err := os.Chmod(progBin, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		cmd := runArm64Bin(qemu, progBin)
		out, _ := cmd.CombinedOutput()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("arm64-android program exited %d, want 42 (out=%q)", c, out)
		}
	})

	t.Run("emit-target-wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping -target wasm32-wasi")
		}
		srcPath := filepath.Join(dir, "wasm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"hi from wasm\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
		if code != 0 {
			t.Fatalf("-target wasm32-wasi emit exited %d, want 0", code)
		}
		if len(wat) == 0 {
			t.Fatal("-target wasm32-wasi produced 0 bytes")
		}
		watPath := filepath.Join(dir, "wasm_prog.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.Output()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("wasm-emitted program exited %d, want 42\n--- WAT ---\n%s", c, wat)
		}
		if string(out) != "hi from wasm\n" {
			t.Errorf("wasm-emitted program stdout = %q, want %q", string(out), "hi from wasm\n")
		}
	})

	t.Run("emit-wasm-core-module", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping -emit core-module")
		}
		srcPath := filepath.Join(dir, "wasmbin_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"hi from a core module\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		outPath := filepath.Join(dir, "wasmbin_prog.wasm")
		stdout, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "core-module", "-o", outPath, srcPath)
		if code != 0 {
			t.Fatalf("-emit core-module emit exited %d, want 0", code)
		}
		if len(stdout) != 0 {
			t.Errorf("-emit core-module with -o wrote %d bytes to stdout, want 0", len(stdout))
		}
		bin, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read core-module output: %v", err)
		}
		if len(bin) < 8 || bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6d {
			t.Fatalf("output is not a wasm binary (magic missing): % x", bin[:min(8, len(bin))])
		}
		if wasmtools, err := exec.LookPath("wasm-tools"); err == nil {
			if out, err := exec.Command(wasmtools, "validate", outPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
			}
		}
		cmd := exec.Command(wasmtime, "run", outPath)
		out, _ := cmd.Output()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("core-module program exited %d, want 42", c)
		}
		if string(out) != "hi from a core module\n" {
			t.Errorf("core-module program stdout = %q, want %q", string(out), "hi from a core module\n")
		}
	})

	t.Run("core-module-opcode-families", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping core-module opcode families")
		}
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		cases := []struct {
			name   string
			src    string
			exit   int
			stdout string
		}{
			{"reinterpret-bits", `function main(): i32 {
    let x: f32 = 1.0 as f32;
    let b: i32 = f32_bits(x);
    let y: f32 = f32_from_bits(b);
    let d: i64 = f64_bits(2.0);
    let z: f64 = f64_from_bits(d);
    if (b == 1065353216 && y == x && z == 2.0 && d == 4611686018427387904) { return 42; }
    return 1;
}
`, 42, ""},
			{"to-string-prints", `import "std/i32";

function main(): i32 {
    print((41i32).to_string());
    return 0;
}
`, 0, "41\n"},
			{"bit-counts", `import "std/i32";
import "std/i64";

function main(): i32 {
    let a: i32 = (16i32).leading_zeros() + (16i32).trailing_zeros() + (255i32).count_ones();
    let b: i32 = (1099511627776i64).leading_zeros() + (1099511627776i64).trailing_zeros() + (255i64).count_ones();
    if (a == 39 && b == 71) { return 42; }
    return 1;
}
`, 42, ""},
			{"bulk-memory-copy", `import "std/string";

function main(): i32 {
    let b: u8[] = "hello".bytes();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < b.len()) { t = t + (b[i] as i32); i = i + 1; }
    if (t == 532 && b.len() == 5) { return 42; }
    return 1;
}
`, 42, ""},
			{"bulk-memory-fill", `function main(): i32 {
    let p: usize = __alloc(8);
    __memset(p, 65, 8);
    if ((__load_i32(p) & 255) == 65) { return 42; }
    return 1;
}
`, 42, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "wasmbin_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				outPath := filepath.Join(dir, "wasmbin_"+tc.name+".wasm")
				if _, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "core-module", "-o", outPath, srcPath, stdlibRoot); code != 0 {
					t.Fatalf("-emit core-module emit exited %d, want 0", code)
				}
				cmd := exec.Command(wasmtime, "run", outPath)
				out, _ := cmd.Output()
				if c := cmd.ProcessState.ExitCode(); c != tc.exit {
					t.Errorf("exited %d, want %d", c, tc.exit)
				}
				if string(out) != tc.stdout {
					t.Errorf("stdout = %q, want %q", string(out), tc.stdout)
				}
			})
		}
	})

	t.Run("emit-wasm-component", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		stdlibRoot, err := filepath.Abs("../../stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		for _, c := range []struct {
			name    string
			src     string
			wantOut string
			stdlib  bool
		}{
			{"no-io", "function main(): i32 { let s = 0; let i = 0; while (i < 7) { s = s + i; i = i + 1; } return s - 21; }\n", "", false},
			{"hex-immediates", "function main(): i32 { let wide = 0xFFFFFFFF; let upper = 0X80000000; let word: u32 = 0xffffffff; if (wide / 65536 == 65535 && upper > 0 && word + 1 == 0u32) { return 0; } return 1; }\n", "", false},
			{"stdout", "function main(): i32 { print(\"hi from component\"); return 0; }\n", "hi from component\n", false},
			{"map", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 1); if (!m.contains(\"a\")) { return 1; } print(\"map ok\"); return 0; }\n", "map ok\n", true},
			{"typed-only", "function picked(k: i32): i32 { let rows: i32[] = [k, k]; let some: i32[] = (match (Some(rows)) { Some(r) => r, None => [] }); return some.len(); }\nfunction main(): i32 { return picked(4) - 2; }\n", "", false},
			{"i64-map-method", "import \"std/array\";\nfunction dbl(x: i64): i64 { return x * (2 as i64); }\nfunction twice(own xs: i64[]): i64[] { return xs.map(dbl); }\nfunction main(): i32 { return twice([1 as i64, 2 as i64]).len() - 2; }\n", "", true},
		} {
			srcPath := filepath.Join(dir, "comp_"+c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "comp_"+c.name+".wasm")
			args := []string{"-target", "wasm32-wasi", "-o", outPath, srcPath}
			if c.stdlib {
				args = append(args, stdlibRoot)
			}
			stdout, code := runDriver(t, args...)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", c.name, code)
			}
			if len(stdout) != 0 {
				t.Errorf("%s: the component with -o wrote %d bytes to stdout, want 0", c.name, len(stdout))
			}
			bin, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("%s: read component: %v", c.name, err)
			}
			if len(bin) < 8 || bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6d || bin[6] != 0x01 {
				t.Fatalf("%s: output is not a wasm component: % x", c.name, bin[:min(8, len(bin))])
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", c.name, err, out)
				}
			}
			cmd := exec.Command(wasmtime, "run", outPath)
			out, _ := cmd.Output()
			if ec := cmd.ProcessState.ExitCode(); ec != 0 {
				t.Errorf("%s: component exited %d, want 0 (main returns 0)", c.name, ec)
			}
			if string(out) != c.wantOut {
				t.Errorf("%s: component stdout = %q, want %q", c.name, string(out), c.wantOut)
			}
		}
	})

	t.Run("emit-wasm-component-fs", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component fs")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		build := func(t *testing.T, name, src string) string {
			t.Helper()
			srcPath := filepath.Join(dir, "compfs_"+name+".fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "compfs_"+name+".wasm")
			_, code := runDriver(t, "-target", "wasm32-wasi", "-o", outPath, srcPath)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", name, code)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", name, err, out)
				}
			}
			return outPath
		}

		fsDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(fsDir, "in.txt"), []byte("hello fs"), 0o644); err != nil {
			t.Fatalf("write in.txt: %v", err)
		}
		readBin := build(t, "read", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } }\n")
		out, _ := exec.Command(wasmtime, "run", "--dir", fsDir+"::/", readBin).Output()
		if string(out) != "hello fs" {
			t.Errorf("read: component stdout = %q, want %q", string(out), "hello fs")
		}

		noDirBin := build(t, "nodir", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { return 1; }, Err(e) => { match (e) { NotFound(_) => { write(\"not found\"); return 0; }, _ => { return 2; } } } } }\n")
		noDirCmd := exec.Command(wasmtime, "run", noDirBin)
		noDirOut, _ := noDirCmd.Output()
		if ec := noDirCmd.ProcessState.ExitCode(); ec != 0 || string(noDirOut) != "not found" {
			t.Errorf("no preopen: component exited %d with stdout %q, want 0 and %q", ec, string(noDirOut), "not found")
		}

		writeBin := build(t, "write", "function main(): i32 { match (write_file(\"out.txt\", \"written\")) { Err(e) => { return 1; }, Ok(_) => {} } return 0; }\n")
		wDir := t.TempDir()
		if ec := exec.Command(wasmtime, "run", "--dir", wDir+"::/", writeBin).Run(); ec != nil {
			t.Fatalf("write: wasmtime run failed: %v", ec)
		}
		if got, err := os.ReadFile(filepath.Join(wDir, "out.txt")); err != nil || string(got) != "written" {
			t.Errorf("write: out.txt = %q (err %v), want %q", string(got), err, "written")
		}

		rwBin := build(t, "rw", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { match (write_file(\"out.txt\", s)) { Err(e) => { return 2; }, Ok(_) => {} } return 0; }, Err(e) => { return 1; } } }\n")
		rwDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(rwDir, "in.txt"), []byte("copy me"), 0o644); err != nil {
			t.Fatalf("write in.txt: %v", err)
		}
		if ec := exec.Command(wasmtime, "run", "--dir", rwDir+"::/", rwBin).Run(); ec != nil {
			t.Fatalf("rw: wasmtime run failed: %v", ec)
		}
		if got, err := os.ReadFile(filepath.Join(rwDir, "out.txt")); err != nil || string(got) != "copy me" {
			t.Errorf("rw: out.txt = %q (err %v), want %q", string(got), err, "copy me")
		}

		fdBin := build(t, "fdloop", `function round(): i32 {
    let s: string = "line\n";
    match (write_file("f.txt", s)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (read_file("f.txt")) { Ok(t) => { if (t.len() != s.len()) { return 2; } }, Err(_) => { return 2; } }
    match (read_file_bytes("f.txt")) { Ok(b) => { if (b.len() != s.len()) { return 3; } }, Err(_) => { return 3; } }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 200i32) {
        let r: i32 = round();
        if (r != 0i32) { write("FAIL"); return r; }
        i = i + 1i32;
    }
    write("fds-released");
    return 0;
}
`)
		fdDir := t.TempDir()
		fdOut, _ := exec.Command("sh", "-c", `ulimit -n 64 && exec "$0" run --dir "$1::/" "$2"`, wasmtime, fdDir, fdBin).Output()
		if string(fdOut) != "fds-released" {
			t.Errorf("fd-release: component stdout = %q, want %q — a file body kept its descriptor or stream", string(fdOut), "fds-released")
		}
	})

	t.Run("emit-wasm-component-wasi", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component wasi")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		build := func(t *testing.T, name, src string) string {
			t.Helper()
			srcPath := filepath.Join(dir, "compw_"+name+".fern")
			if err := os.WriteFile(srcPath, []byte(withPrintInt(src)), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "compw_"+name+".wasm")
			_, code := runDriver(t, "-target", "wasm32-wasi", "-o", outPath, srcPath)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", name, code)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", name, err, out)
				}
			}
			return outPath
		}

		envBin := build(t, "env", "function main(): i32 { match (env(\"API_KEY\")) { Some(v) => { write(v); return 0; }, None => { write(\"MISS\"); return 1; } } }\n")
		if out, _ := exec.Command(wasmtime, "run", "--env", "API_KEY=sk-xyz", envBin).Output(); string(out) != "sk-xyz" {
			t.Errorf("env: stdout = %q, want %q", string(out), "sk-xyz")
		}

		argsBin := build(t, "args", "function main(): i32 { print_int(args().len()); return 0; }\n")
		if out, _ := exec.Command(wasmtime, "run", argsBin, "one", "two").Output(); string(out) != "3" {
			t.Errorf("args: stdout = %q, want %q (argv0 + 2)", string(out), "3")
		}

		clockBin := build(t, "clock", "function main(): i32 { let t: i64 = now_unix_ms(); if (t > 0) { write(\"ok\"); return 0; } return 1; }\n")
		if out, _ := exec.Command(wasmtime, "run", clockBin).Output(); string(out) != "ok" {
			t.Errorf("clock: stdout = %q, want %q", string(out), "ok")
		}

		monoBin := build(t, "mono", "function main(): i32 { let t: i64 = monotonic_ns(); if (t > 0) { write(\"ok\"); return 0; } return 1; }\n")
		if out, _ := exec.Command(wasmtime, "run", monoBin).Output(); string(out) != "ok" {
			t.Errorf("clock-mono: stdout = %q, want %q", string(out), "ok")
		}

		rndBin := build(t, "rnd", "function main(): i32 { let x: i32 = random_i32(); if (x == x) { write(\"ok\"); return 0; } return 1; }\n")
		if out, _ := exec.Command(wasmtime, "run", rndBin).Output(); string(out) != "ok" {
			t.Errorf("random: stdout = %q, want %q", string(out), "ok")
		}

		epBin := build(t, "eprint", "function main(): i32 { eprint(\"to stderr\"); return 0; }\n")
		{
			cmd := exec.Command(wasmtime, "run", epBin)
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if !strings.Contains(eb.String(), "to stderr") {
				t.Errorf("eprint: stderr = %q, want it to contain %q", eb.String(), "to stderr")
			}
		}

		exitBin := build(t, "exit", "function main(): i32 { write(\"before\"); exit(0); return 0; }\n")
		{
			cmd := exec.Command(wasmtime, "run", exitBin)
			out, _ := cmd.Output()
			if string(out) != "before" {
				t.Errorf("exit: stdout = %q, want %q", string(out), "before")
			}
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("exit: exit code = %d, want 0", code)
			}
		}
		exit3Bin := build(t, "exit3", "function main(): i32 { write(\"before\"); exit(3); return 0; }\n")
		{
			cmd := exec.Command(wasmtime, "run", exit3Bin)
			out, _ := cmd.Output()
			if string(out) != "before" {
				t.Errorf("exit(3): stdout = %q, want %q", string(out), "before")
			}
			if code := cmd.ProcessState.ExitCode(); code != 1 {
				t.Errorf("exit(3): exit code = %d, want 1", code)
			}
		}
	})

	t.Run("emit-wasm-component-combos", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component combos")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		build := func(t *testing.T, name, src string) string {
			t.Helper()
			srcPath := filepath.Join(dir, "compc_"+name+".fern")
			if err := os.WriteFile(srcPath, []byte(withPrintInt(src)), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "compc_"+name+".wasm")
			_, code := runDriver(t, "-target", "wasm32-wasi", "-o", outPath, srcPath)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", name, code)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", name, err, out)
				}
			}
			return outPath
		}
		mkdir := func(t *testing.T) string {
			d := t.TempDir()
			if err := os.WriteFile(filepath.Join(d, "in.txt"), []byte("DATA"), 0o644); err != nil {
				t.Fatalf("write in.txt: %v", err)
			}
			return d
		}

		readEnv := build(t, "read_env", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { match (env(\"P\")) { Some(v) => { write(v); write(s); return 0; }, None => { write(s); return 0; } } }, Err(e) => { return 1; } } }\n")
		d1 := mkdir(t)
		if out, _ := exec.Command(wasmtime, "run", "--dir", d1+"::/", "--env", "P=X-", readEnv).Output(); string(out) != "X-DATA" {
			t.Errorf("read+env: stdout = %q, want %q", string(out), "X-DATA")
		}

		rwEnv := build(t, "rw_env", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { match (env(\"P\")) { Some(v) => { match (write_file(\"out.txt\", v)) { Err(e) => { return 2; }, Ok(_) => {} } return 0; }, None => { return 3; } } }, Err(e) => { return 1; } } }\n")
		d2 := mkdir(t)
		if ec := exec.Command(wasmtime, "run", "--dir", d2+"::/", "--env", "P=ENV", rwEnv).Run(); ec != nil {
			t.Fatalf("rw+env: run failed: %v", ec)
		}
		if got, _ := os.ReadFile(filepath.Join(d2, "out.txt")); string(got) != "ENV" {
			t.Errorf("rw+env: out.txt = %q, want %q", string(got), "ENV")
		}

		rndWrite := build(t, "rnd_write", "function main(): i32 { let x: i32 = random_i32(); if (x != x) { return 9; } match (write_file(\"out.txt\", \"R\")) { Err(e) => { return 1; }, Ok(_) => {} } return 0; }\n")
		d3 := t.TempDir()
		if ec := exec.Command(wasmtime, "run", "--dir", d3+"::/", rndWrite).Run(); ec != nil {
			t.Fatalf("random+write: run failed: %v", ec)
		}
		if got, _ := os.ReadFile(filepath.Join(d3, "out.txt")); string(got) != "R" {
			t.Errorf("random+write: out.txt = %q, want %q", string(got), "R")
		}

		readArgs := build(t, "read_args", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { print_int(args().len()); write(s); return 0; }, Err(e) => { return 1; } } }\n")
		d4 := mkdir(t)
		if out, _ := exec.Command(wasmtime, "run", "--dir", d4+"::/", readArgs, "a").Output(); string(out) != "2DATA" {
			t.Errorf("read+args: stdout = %q, want %q (argv0+1, then DATA)", string(out), "2DATA")
		}

		rwArgs := build(t, "rw_args", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { let n = args().len(); match (write_file(\"out.txt\", s)) { Err(e) => { return 2; }, Ok(_) => {} } return n - n; }, Err(e) => { return 1; } } }\n")
		d5 := mkdir(t)
		if ec := exec.Command(wasmtime, "run", "--dir", d5+"::/", rwArgs, "x", "y").Run(); ec != nil {
			t.Fatalf("rw+args: run failed: %v", ec)
		}
		if got, _ := os.ReadFile(filepath.Join(d5, "out.txt")); string(got) != "DATA" {
			t.Errorf("rw+args: out.txt = %q, want %q", string(got), "DATA")
		}
	})

	t.Run("wasm-component-multi-wasi-composes", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
		srcPath := filepath.Join(dir, "comp_multi.fern")
		src := "function main(): i32 { let n = args().len(); match (env(\"X\")) { Some(v) => { if (n == 3) { print(\"set3\"); } return 0; }, None => { if (n == 1) { print(\"unset1\"); } return 0; } } }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		comp := filepath.Join(dir, "comp_multi.wasm")
		if out, code := runDriver(t, "-target", "wasm32-wasi", "-o", comp, srcPath); code != 0 {
			t.Fatalf("wasm-component on a multi-WASI program exited %d:\n%s", code, out)
		}
		if out, err := exec.Command(wasmtime, "run", "--env", "X=1", comp, "a", "b").Output(); err != nil || string(out) != "set3\n" {
			t.Errorf("with X and two args: stdout = %q (%v), want %q", out, err, "set3\n")
		}
		if out, err := exec.Command(wasmtime, "run", comp).Output(); err != nil || string(out) != "unset1\n" {
			t.Errorf("without X: stdout = %q (%v), want %q", out, err, "unset1\n")
		}
	})

	t.Run("wasm-component-refuses-extern-outside-world", func(t *testing.T) {
		refused := filepath.Join(dir, "comp_refused.fern")
		if err := os.WriteFile(refused, []byte("@import(\"local:test/sink@0.1.0\", \"pick\")\nfunction pick(c: i32): i32;\nfunction main(): i32 { return pick(1); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "comp_refused.wasm"), refused)
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 2 || !strings.Contains(string(out), "local:test/sink@0.1.0") {
			t.Errorf("exit %d, output %q; want 2 naming the interface", code, out)
		}
	})

	t.Run("wasm-unsupported-builtin-names-the-builtin", func(t *testing.T) {
		runtimeBin := e2eharness.RuntimeCLI(t)
		for _, tc := range []struct {
			name    string
			src     string
			mustSay []string
		}{
			{"syscall-floor", "function main(): i32 { let r: i64 = __syscall3(1, 1, 0, 0); return 0; }\n", []string{"__syscall3", "E066", "syscall"}},
			{"raw-memory-floor", "function main(): i32 { let p: usize = __raw_alloc(64); return 0; }\n", []string{"__raw_alloc", "E066", "rawmem"}},
			{"timer-fd", "function main(): i32 { return timer_fd(10); }\n", []string{"timer_fd", "E066", "pollfd"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "unsupported_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				cmd := exec.Command(runtimeBin, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				_ = cmd.Run()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("driver did not exit normally")
				}
				if code := cmd.ProcessState.ExitCode(); code != 1 {
					t.Errorf("exit = %d, want 1; stderr:\n%s", code, stderr.String())
				}
				for _, want := range tc.mustSay {
					if !strings.Contains(stderr.String(), want) {
						t.Errorf("stderr does not mention %q while refusing on wasm:\n%s", want, stderr.String())
					}
				}
				if strings.Contains(stdout.String(), "(module") {
					t.Errorf("refused but still emitted a module (%d bytes)", stdout.Len())
				}
			})
		}

		srcPath := filepath.Join(dir, "unsupported_syscall-floor.fern")
		out, err := exec.Command(runtimeBin, "-target", "x86-64-linux", "-emit", "asm", srcPath).CombinedOutput()
		if err != nil {
			t.Fatalf("x86-64 refused a runtime program it lowers: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), ".globl") {
			t.Errorf("x86-64 emitted no assembly for the syscall program (%d bytes)", len(out))
		}
	})

	t.Run("unknown-target-exits-2", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret42.fern")
		_, code := runDriver(t, "-target", "riscv", srcPath)
		if code != 2 {
			t.Errorf("unknown -target exited %d, want 2", code)
		}
	})

	t.Run("no-arg-exits-2", func(t *testing.T) {
		_, code := runDriver(t)
		if code != 2 {
			t.Errorf("no-arg driver exited %d, want 2", code)
		}
	})

	t.Run("unknown-flag-exits-2", func(t *testing.T) {
		_, code := runDriver(t, "-nope", filepath.Join(dir, "ret42.fern"))
		if code != 2 {
			t.Errorf("unknown-flag driver exited %d, want 2", code)
		}
	})

	t.Run("flag-refusals-exit-2", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "main.fern")
		if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		so := filepath.Join(tmp, "x.so")
		for _, args := range [][]string{
			{"-target", "wasm32-wasi", "-shared", "-o", so, src},
			{"-target", "x86-64-linux", "-shared", src},
			{"-target", "x86-64-linux", "-shared", "-export", "nope", "-o", so, src},
			{"-target", "x86-64-linux", "-harden", "-o", so, src},
			{"-target", "wasm32-wasi-http", "-run", src},
			{"-target", "wasm32-wasi", "-cover", src},
		} {
			if _, code := runDriver(t, args...); code != 2 {
				t.Errorf("fern %s exited %d, want 2", strings.Join(args, " "), code)
			}
		}
		cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", so, src)
		cmd.Env = append(os.Environ(), "FERN_PROFILE=bogus")
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 2 {
			t.Errorf("FERN_PROFILE=bogus exited %d, want 2", code)
		}
	})

	t.Run("missing-file-exits-1", func(t *testing.T) {
		_, code := runDriver(t, filepath.Join(dir, "does-not-exist.fern"))
		if code != 1 {
			t.Errorf("missing-file driver exited %d, want 1", code)
		}
	})
}

var (
	watCallRe = regexp.MustCompile(`call \$([A-Za-z0-9_.$]+)`)
	watFuncRe = regexp.MustCompile(`\(func \$([A-Za-z0-9_.$]+)`)
)

func undefinedWatCalls(wat []byte) []string {
	defined := map[string]bool{}
	for _, m := range watFuncRe.FindAllSubmatch(wat, -1) {
		defined[string(m[1])] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range watCallRe.FindAllSubmatch(wat, -1) {
		name := string(m[1])
		if defined[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}
