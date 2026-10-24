package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const brotliEncoderMain = `import "std/brotli";
import "std/array";
function main(): i32 {
 for n in [0, 1, 7, 31, 63, 1024, 65535, 65536, 65537, 131072] {
  let input: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) { input = input.with(i, (i % 23) as u8); i = i + 1; }
  let encoded: u8[] = brotli.compress(input);
  let Ok(decoded) = brotli.decompress(encoded, n) else { return 1; };
  if (decoded.len() != n) { return 2; }
  i = 0;
  while (i < n) { if (decoded[i] != input[i]) { return 3; } i = i + 1; }
  if (n >= 1024 && encoded.len() > n / 4) { return 4; }
 }
 return 0;
}`

func TestSelfHostBrotliEncoder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(brotliEncoderMain), 0600); err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, path)
}

func TestSelfHostBrotliEncoderIndependent(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("independent codec gate runs on x86-64 Linux")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for independent Brotli decoding")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.fern")
	const source = `import "std/brotli";
import "std/result";
function main(): i32 {
 let Ok(input) = read_file_bytes(args()[1]) else {return 1;};
 let encoded = brotli.compress(input);
 let Ok(_) = write_file_bytes(args()[2], encoded) else {return 2;};
 return 0;
}`
	if err := os.WriteFile(entry, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, entry)
	fixtures := map[string][]byte{"empty": {}, "byte": {255}, "all-bytes": make([]byte, 256), "repeated": bytes.Repeat([]byte("the native Brotli encoder\x00\xff\n"), 6000)}
	for i := range fixtures["all-bytes"] {
		fixtures["all-bytes"][i] = byte(i)
	}
	for _, n := range []int{65535, 65536, 65537, 131072} {
		random := make([]byte, n)
		seed := uint32(12941)
		for i := range random {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			random[i] = byte(seed)
		}
		fixtures[fmt.Sprintf("random-%d", n)] = random
	}
	for _, file := range []string{"web/index.html", "web/shared/showcase.css", "rally.fern.md"} {
		data, err := os.ReadFile(filepath.Join("../../../examples/showcase", file))
		if err != nil {
			t.Fatal(err)
		}
		fixtures[file] = data
	}
	for name, data := range fixtures {
		t.Run(name, func(t *testing.T) {
			input, output := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "output.br")
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := runX86_64Bin(cli.runner, bin, input, output).CombinedOutput(); err != nil {
				t.Fatalf("encode: %v %s", err, out)
			}
			const check = `const fs=require('node:fs'),z=require('node:zlib'),assert=require('node:assert/strict');const raw=fs.readFileSync(process.argv[1]),coded=fs.readFileSync(process.argv[2]);assert.deepEqual(z.brotliDecompressSync(coded),raw);if(raw.length>1000&&raw[0]===60)assert.ok(coded.length<raw.length*0.8);`
			if out, err := exec.Command(node, "-e", check, input, output).CombinedOutput(); err != nil {
				t.Fatalf("independent decode: %v %s", err, out)
			}
		})
	}
}
