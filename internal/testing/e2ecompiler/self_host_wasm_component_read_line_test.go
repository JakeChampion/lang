package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmComponentReadLine builds programs that read stdin through
// io.read_line(stdin()) as wasm32-wasi components with the self-host CLI. The
// stdin() call is all that says the component needs wasi:cli/stdin: the CLI
// once refused a read-only program as having no import for it, and composed
// one that also printed without fetching the stdin stream, so its read saw
// EOF.
//
// A line keeps its newline, as the interpreter's does, a last line without one
// is still returned, and EOF is Ok(None). A component reports only 0 or non-zero,
// so each program's answer is that and what it prints.
func TestSelfHostWasmComponentReadLine(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	type run struct {
		stdin, stdout string
		ok            bool
	}
	cases := []struct {
		name, src string
		runs      []run
	}{
		{"read_line", `import "std/io";
function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { return 0; },
        _ => { return 1; }
    }
    return 1;
}`, []run{{"hello\n", "", true}, {"", "", false}}},
		{"read_line print", `import "std/io";
function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { print(line); return 0; },
        _ => { return 1; }
    }
    return 1;
}`, []run{{"hello\n", "hello\n\n", true}, {"", "", false}}},
		{"read_line exit", `import "std/io";
function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { exit(0); return 0; },
        _ => { exit(1); return 1; }
    }
    return 5;
}`, []run{{"hi\n", "", true}, {"", "", false}}},
		{"read_line print exit", `import "std/io";
function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { print(line); exit(0); return 0; },
        _ => { exit(2); return 2; }
    }
    return 5;
}`, []run{{"echo me\n", "echo me\n\n", true}, {"", "", false}}},
		{"every line", `import "std/io";
function main(): i32 {
    let n: i32 = 0;
    let done: boolean = false;
    while (!done) {
        match (io.read_line(stdin())) {
            Ok(Some(line)) => { print("[" + line + "]"); n = n + 1; },
            _ => { done = true; }
        }
    }
    if (n == 3) { return 0; }
    return 1;
}`, []run{{"one\ntwo\nthree", "[one\n]\n[two\n]\n[three]\n", true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, comp := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, src, cli.stdlib)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host component: %v\n%s", err, out)
			}
			if out, err := exec.Command("wasm-tools", "validate", comp).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, out)
			}
			printed, err := exec.Command("wasm-tools", "print", comp).CombinedOutput()
			if err != nil {
				t.Fatalf("wasm-tools print: %v\n%s", err, printed)
			}
			if !strings.Contains(string(printed), `(import "wasi:cli/stdin@0.2.0"`) {
				t.Errorf("component does not import wasi:cli/stdin@0.2.0")
			}
			for _, r := range tc.runs {
				run := exec.Command("wasmtime", "run", comp)
				run.Stdin = strings.NewReader(r.stdin)
				var stdout, stderr bytes.Buffer
				run.Stdout, run.Stderr = &stdout, &stderr
				err := run.Run()
				if ok := err == nil; ok != r.ok {
					t.Errorf("stdin %q: exit ok = %v, want %v (%v)\nstderr:\n%s", r.stdin, ok, r.ok, err, stderr.String())
				}
				if got := stdout.String(); got != r.stdout {
					t.Errorf("stdin %q: stdout = %q, want %q", r.stdin, got, r.stdout)
				}
			}
		})
	}
}
