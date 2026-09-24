package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wasm runtime's I/O helpers gave back none of their scratch (#9305):
// strbuf abandoned every outgrown buffer and kept the last one, read_line its
// 256-byte line buffer, stat and fd_stat their 64-byte record, and
// remove_dir_all a 4 KiB readdir buffer per directory, every child path, the
// name array and every recursive result box. Each program below touches one
// helper and has to balance.
func TestSelfHostWasmIOScratchReleased(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm I/O scratch")
	}
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name, src, stdin string
		want             int
		setup            func(t *testing.T, dir string)
	}{
		{"strbuf_grows", `function main(): i32 {
    strbuf_reset();
    var i: i32 = 0;
    while (i < 100) { strbuf_append("0123456789"); i = i + 1; }
    var t: string = strbuf_take();
    strbuf_append("again");
    var u: string = strbuf_take();
    return (t.len() + u.len()) % 101;
}`, "", (1000 + 5) % 101, nil},
		{"read_line", `function main(): i32 {
    var n: i32 = 0;
    match (read_line()) { Some(l) => { n = l.len(); }, None => { n = 50; } }
    match (read_line()) { Some(l) => { n = n + 100; }, None => { n = n + 1; } }
    return n;
}`, "hello\n", 7, nil},
		{"stat", `function main(): i32 {
    var n: i32 = 0;
    match (stat("present.txt")) { Ok(st) => { n = n + 1; }, Err(e) => { n = n + 10; } }
    match (stat("absent.txt")) { Ok(st) => { n = n + 100; }, Err(e) => { n = n + 2; } }
    return n;
}`, "", 3, func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "present.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"remove_dir_all", `function main(): i32 {
    match (remove_dir_all("tree")) { Ok(_) => { return 1; }, Err(e) => { return 0; } }
    return 5;
}`, "", 1, func(t *testing.T, dir string) {
			for _, d := range []string{"tree/a/b", "tree/c"} {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range []string{"tree/f", "tree/a/g", "tree/a/b/h"} {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			wasm := filepath.Join(dir, "prog.wasm")
			build := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "core-module", src, cli.stdlib, "-o", wasm)
			build.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			run := exec.Command("wasmtime", "run", "--dir=.", wasm)
			run.Dir = dir
			run.Stdin = strings.NewReader(tc.stdin)
			var eb strings.Builder
			run.Stderr = &eb
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", code, tc.want, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}
