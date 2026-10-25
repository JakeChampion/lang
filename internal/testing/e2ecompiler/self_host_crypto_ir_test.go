package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var cryptoModuleIRCases = []struct {
	name string
	src  string
}{
	{"sha256-abc", `import "std/crypto";
import "std/hex";
function main(): i32 { if (hex.encode(crypto.sha256("abc".as_bytes())) == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 42; } return 0; }`},
	{"sha256-empty", `import "std/crypto";
import "std/hex";
function main(): i32 { if (hex.encode(crypto.sha256("".as_bytes())) == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") { return 42; } return 0; }`},
	{"sha256-pangram-len", `import "std/crypto";
import "std/hex";
function main(): i32 { return hex.encode(crypto.sha256("The quick brown fox jumps over the lazy dog".as_bytes())).len(); }`},
	{"hmac-known", `import "std/crypto";
import "std/hex";
function main(): i32 { if (hex.encode(crypto.hmac_sha256([107 as u8, 101 as u8, 121 as u8], "The quick brown fox jumps over the lazy dog".as_bytes())) == "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") { return 42; } return 0; }`},
}

func TestSelfHostCryptoModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range cryptoModuleIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "crypto_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "crypto_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
