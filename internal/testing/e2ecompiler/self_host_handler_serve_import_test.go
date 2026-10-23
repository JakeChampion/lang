package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostHandlerMissingServeImport(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "handler.fern")
	const source = "import \"std/http\";\nimport \"std/platform\";\n\nfunction handle(req: HttpRequest, plat: platform.Platform): HttpResponse { return http.plain(\"hello\"); }\n"
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-darwin", "wasm32-wasi"} {
		for _, mode := range []string{"check", "interp", "compile"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				extra := []string{}
				if mode != "compile" {
					extra = append(extra, "-"+mode)
				}
				dest := filepath.Join(t.TempDir(), "out")
				cmd := cli.compileCmd(target, dest, src, extra...)
				out, err := cmd.CombinedOutput()
				if err == nil || cmd.ProcessState.ExitCode() != 1 {
					t.Fatalf("exit: %v\n%s", err, out)
				}
				message := string(out)
				if strings.Count(message, "error[E001]") != 1 || !strings.Contains(message, "handler.fern:4:1: error[E001]: a top-level `handle` requires `std/serve`") {
					t.Fatalf("missing import diagnostic:\n%s", out)
				}
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatalf("created an output: %v", err)
				}
			})
		}
	}
	t.Run("wasi-http", func(t *testing.T) {
		out, err := cli.compileCmd("wasm32-wasi-http", filepath.Join(t.TempDir(), "out.wasm"), src).CombinedOutput()
		if err != nil {
			t.Fatalf("HTTP component: %v\n%s", err, out)
		}
	})
	for _, accepted := range []string{"import \"std/serve\";\n" + source, "import \"std/serve.fern\" as server;\n" + source, source + "\nfunction main(): i32 { return 0; }\n"} {
		if err := os.WriteFile(src, []byte(accepted), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cli.compileCmd("x86-64-linux", filepath.Join(dir, "unused"), src, "-check").CombinedOutput()
		if err != nil {
			t.Fatalf("serve import: %v\n%s", err, out)
		}
	}
}

func TestSelfHostExecutableNeedsMain(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "library.fern")
	if err := os.WriteFile(src, []byte("pub function answer(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-darwin", "wasm32-wasi"} {
		out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), src).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "library.fern:1:1: error[E001]: an executable requires a top-level `main` or `handle`") {
			t.Fatalf("%s: %v\n%s", target, err, out)
		}
	}
	out, err := cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "unused"), src, "-check").CombinedOutput()
	if err != nil {
		t.Fatalf("library check: %v\n%s", err, out)
	}
	out, err = cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "lib.so"), src, "-shared", "-export", "answer").CombinedOutput()
	if err != nil {
		t.Fatalf("shared library: %v\n%s", err, out)
	}
	out, err = cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "library.s"), src, "-emit", "asm").CombinedOutput()
	if err != nil {
		t.Fatalf("assembly source: %v\n%s", err, out)
	}
	if err := os.WriteFile(src, []byte("print(\"script\");\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "script"), src).CombinedOutput()
	if err != nil {
		t.Fatalf("script entry: %v\n%s", err, out)
	}
}
