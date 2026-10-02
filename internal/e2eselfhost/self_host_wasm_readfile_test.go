package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// readFileLargeSrc reads a 1.5 MB file whose byte k is 48 + k % 61 and checks
// every byte. The two strings freed first leave a 64 KiB-class and a 1 MiB-class
// block for the reader's first buffer to reuse, with the live sentinel `b`
// right after them: a reader that writes past its own block overwrites `b`
// or its own input. The preview-1 reader once grew its buffer by allocating
// the next block and assuming it was adjacent, and the preview-2 one read into
// a fixed 1 MiB buffer.
const readFileLargeSrc = `import "std/string";

function main(): i32 {
    var a: string = "y".repeat(65530);
    var c: string = "y".repeat(1048570);
    var b: string = "z".repeat(100);
    a = "";
    c = "";
    match (read_file("big.txt")) {
        Ok(s) => {
            if (s.len() != 1500000) { return 1; }
            var k: i32 = 0;
            while (k < s.len()) {
                if (s[k] as i32 != 48 + k % 61) { return 2; }
                k = k + 1;
            }
        },
        Err(e) => { return 9; }
    }
    if (b != "z".repeat(100)) { return 4; }
    return 0;
}
`

// TestSelfHostWasmReadFileLarge runs readFileLargeSrc as a preview-1 command
// core and as a preview-2 component. Exit 1 is a wrong length, 2 a wrong
// byte, 4 an overwritten sentinel, 9 an Err.
func TestSelfHostWasmReadFileLarge(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm read_file e2e")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	content := make([]byte, 1500000)
	for k := range content {
		content[k] = byte(48 + k%61)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "rf.fern")
	if err := os.WriteFile(src, []byte(readFileLargeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "rf.wasm")
	build := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", component, src, cli.stdlib)
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build component: %v\n%s", err, msg)
	}
	for _, form := range []struct{ name, module string }{
		{"core", cli.emit(t, src, "wasm32-wasi")},
		{"component", component},
	} {
		t.Run(form.name, func(t *testing.T) {
			out, err := exec.Command(wasmtime, "run", "--dir", dir+"::.", form.module).CombinedOutput()
			if err != nil {
				t.Fatalf("read_file of a 1.5 MB file: %v\n%s", err, out)
			}
		})
	}
}
