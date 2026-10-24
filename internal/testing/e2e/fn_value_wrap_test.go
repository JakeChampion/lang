package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const fnValueWrapSrc = `import "std/i32";
@noinline function twice(x: i32): i32 { return x * 2; }
@noinline function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
@noinline function first(x: i32): i32 { return apply(twice, x); }
@noinline function second(x: i32): i32 { return apply(twice, x + 1); }
@noinline function third(x: i32): i32 {
  let g = twice;
  return apply(g, x + 2);
}
function main(): i32 {
  print((first(1) + second(1) + third(1)).to_string());
  return 0;
}
`

const fnValueWrapWant = "12\n"

func TestFnValueWrapSharedPerFunction(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(fnValueWrapSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	shared := regexp.MustCompile(`(?m)^\S*twice\$wrap:`)
	perSite := regexp.MustCompile(`(?m)^\S*(first|second|third)\$wrap\d+:`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm := filepath.Join(dir, target+".s")
		if out, err := exec.Command(buildLangBinForInterp(t), "-target", target, "-emit", "asm", "-o", asm, src).CombinedOutput(); err != nil {
			t.Fatalf("%s emit: %v\n%s", target, err, out)
		}
		text, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(shared.FindAll(text, -1)); n != 1 {
			t.Errorf("%s: %d trampolines named for twice, want 1", target, n)
		}
		if m := perSite.FindAll(text, -1); len(m) != 0 {
			t.Errorf("%s: a taking function still mints its own trampoline: %q", target, m)
		}
	}
}

func TestInterpFnValueWrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(fnValueWrapSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != fnValueWrapWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, fnValueWrapWant, errOut)
	}
}

func TestX86_64FnValueWrap(t *testing.T) {
	out, code := compileAndRunX86_64(t, fnValueWrapSrc)
	if code != 0 || out != fnValueWrapWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, fnValueWrapWant)
	}
}

func TestArm64FnValueWrap(t *testing.T) {
	out, code := compileAndRunArm64(t, fnValueWrapSrc)
	if code != 0 || out != fnValueWrapWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, fnValueWrapWant)
	}
}

func TestWasmFnValueWrap(t *testing.T) {
	if out := compileAndRunWasmCapture(t, fnValueWrapSrc); out != fnValueWrapWant {
		t.Fatalf("printed %q, want %q", out, fnValueWrapWant)
	}
}

func TestFnValueWrapAcrossModules(t *testing.T) {
	dir := t.TempDir()
	lib := "@noinline pub function twice(x: i32): i32 { return x * 2; }\n" +
		"@noinline pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }\n" +
		"@noinline pub function via_lib(x: i32): i32 { return apply(twice, x); }\n"
	main := "import \"./util\";\nimport \"std/i32\";\n" +
		"function main(): i32 {\n" +
		"  print((util.via_lib(1) + util.apply(util.twice, 2)).to_string());\n" +
		"  return 0;\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "util.fern"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildLangBinForInterp(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		out := filepath.Join(dir, "prog-"+target)
		cmd := exec.Command(bin, "-target", target, "-o", out, src)
		cmd.Dir = dir
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s build: %v\n%s", target, err, o)
		}
		if target != "x86-64-linux" || runtime.GOARCH != "amd64" {
			continue
		}
		got, err := exec.Command(out).Output()
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if strings.TrimSpace(string(got)) != "6" {
			t.Fatalf("printed %q, want 6", got)
		}
	}
}
