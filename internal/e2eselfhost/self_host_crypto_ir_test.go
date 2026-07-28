package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/crypto (SHA-256 + HMAC-SHA256) on the self-host IR path. This module was
// the ORIGINAL motivating gap of the integer-formatting work: it used to crash
// the self-hosted compiler at runtime (see the 2026-06-22 std/crypto FEATURE-
// AUDIT entry — "self-host-gated NOT", interp-only). The crash root-caused to
// the 64-bit integer paths (the as_i64/as_u64 truncation and u64 to_string),
// which are now fixed — so std/crypto compiles through the self-host IR path and
// produces the correct digests. These cases pin the FIPS 180-4 / RFC known-answer
// vectors: each routes "ir" through the self-hosted x86-64 loader (asm_load_run)
// with the real stdlib root and matches the native interpreter.
var cryptoModuleIRCases = []struct {
	name string
	src  string
}{
	// SHA-256("abc") = ba7816bf…20015ad (FIPS 180-4).
	{"sha256-abc", `import "std/crypto";
function main(): i32 { if (crypto.sha256_hex("abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 42; } return 0; }`},
	// SHA-256("") = e3b0c442…7852b855 (the empty-string vector).
	{"sha256-empty", `import "std/crypto";
function main(): i32 { if (crypto.sha256_hex("") == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") { return 42; } return 0; }`},
	// SHA-256 of the classic pangram → 64 hex chars.
	{"sha256-pangram-len", `import "std/crypto";
function main(): i32 { return crypto.sha256_hex("The quick brown fox jumps over the lazy dog").len(); }`},
	// HMAC-SHA256("key", pangram) = f7bc83f4…2d1a3cd8 (well-known RFC-shaped vector).
	{"hmac-known", `import "std/crypto";
function main(): i32 { if (crypto.hmac_sha256_hex([107 as u8, 101 as u8, 121 as u8], "The quick brown fox jumps over the lazy dog") == "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") { return 42; } return 0; }`},
}

func TestSelfHostCryptoModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
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
