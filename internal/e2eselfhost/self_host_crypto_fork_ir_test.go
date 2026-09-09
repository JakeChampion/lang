package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8874: each streaming hasher calls __copy_in(own dst) with a buffer that
// can still belong to a retained state. Fork before completing the block,
// update both branches, and check both digests after both writes. Expected
// digests are independently generated with Python hashlib for abc and abd.
var cryptoForkCases = []struct {
	name, constructor, abc, abd string
}{
	{"md5", "md5_new()", "900150983cd24fb0d6963f7d28e17f72", "4911e516e5aa21d327512e0c8b197616"},
	{"sha1", "sha1_new()", "a9993e364706816aba3e25717850c26c9cd0d89d", "cb4cc28df0fdbe0ecf9d9662e294b118092a5735"},
	{"sha224", "sha224_new()", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7", "9a7b7e67edba75ffa6c9f139c319ca3b5e9cf99cb36979d3c33bf2c8"},
	{"sha256", "sha256_new()", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "a52d159f262b2c6ddb724a61840befc36eb30c88877a4030b65cbe86298449c9"},
	{"sha384", "sha384_new()", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7", "5d15bcebb965fa77926c23471c96e3a326b363f5f105c3ef17cfd033b9734fa46556f81a26bb3044d2dda50481325ef7"},
	{"sha512", "sha512_new()", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f", "1a9840c27a5cf22dab060cdd8a83da2b0fbcb1aeb52d4f9d3894b639083e205a5ab3f6afaeeb21b8e99b5e0fe93daafaabeef274da5d6eadcc9db36e5b6f64c4"},
	{"blake2b", "blake2b_new(64)", "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923", "61ce80f69e6b4300540c5e78254055cb41fefc8dc87af2322bc777db0b9e4f44e03f8cddee63b505060746d5dd86b70a4e3faaf1a2c5d1fd6bf10bed761d4e0d"},
}

func cryptoForkSource(constructor, abc, abd string) string {
	return fmt.Sprintf(`import "std/crypto";
function exercise(): i32 {
    var h = crypto.%s;
    h = h.update("ab");
    var keep = h;
    h = h.update("c");
    keep = keep.update("d");
    if (h.final_hex() != %q) { return 1; }
    if (keep.final_hex() != %q) { return 2; }
    return 0;
}
function main(): i32 {
    var result = exercise();
    if (result != 0) { return result; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`, constructor, abc, abd)
}

func TestSelfHostCryptoForkIRX86_64(t *testing.T) { testSelfHostCryptoForkIR(t, "x86-64-linux") }
func TestSelfHostCryptoForkIRArm64(t *testing.T)  { testSelfHostCryptoForkIR(t, "arm64-linux") }
func TestSelfHostCryptoForkIRWasm(t *testing.T)   { testSelfHostCryptoForkIR(t, "wasm32-wasi") }

func testSelfHostCryptoForkIR(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	var armGCC, armRunner string
	var wasmtime string
	if target == "arm64-linux" {
		armGCC, armRunner = arm64Tooling(t)
	}
	if target == "wasm32-wasi" {
		var err error
		wasmtime, err = exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	dir := copySelfHostTree(t)
	// The production CLI routes all three targets through IR-or-error emitters.
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cryptoForkCases {
		t.Run(tc.name, func(t *testing.T) {
			source := cryptoForkSource(tc.constructor, tc.abc, tc.abd)
			entry := filepath.Join(dir, "fork_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			if target == "x86-64-linux" {
				if _, code := runFixtureInterp(t, entry, ""); code != 0 {
					t.Fatalf("interpreter snapshot contract: exit %d", code)
				}
				if _, code := compileAndRunX86_64(t, source); code != 0 {
					t.Fatalf("native snapshot contract: exit %d", code)
				}
			}
			cmd := runX86_64Bin(runner, driver, "-target", target, "-emit", "asm", entry, root)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			output, err := cmd.Output()
			if err != nil || len(output) == 0 {
				t.Fatalf("self-host compile: %v\n%s", err, diagnostics.String())
			}
			var run *exec.Cmd
			switch target {
			case "x86-64-linux":
				run = runX86_64Bin(runner, buildBin(t, gcc, dir, tc.name, string(output)))
			case "arm64-linux":
				run = runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, tc.name, string(output)))
			case "wasm32-wasi":
				wat := filepath.Join(dir, tc.name+".wat")
				if err := os.WriteFile(wat, output, 0o644); err != nil {
					t.Fatal(err)
				}
				run = exec.Command(wasmtime, "run", wat)
			}
			if output, err := run.CombinedOutput(); err != nil {
				t.Fatalf("self-host snapshot contract: %v\n%s", err, output)
			}
		})
	}
}
