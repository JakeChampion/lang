package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The name the account database gives a uid, or `none` (#9815).
const getpwuidProgram = `function name_of(uid: u32): string {
  let p: usize = __getpwuid_name(uid);
  if (p == 0 as usize) { return "none"; }
  let bs: u8[] = [];
  let at: usize = p;
  while (__load_u8(at) != 0) {
    bs = bs.append(__load_u8(at) as u8);
    at = at + 1 as usize;
  }
  return string_from_bytes_unchecked(bs);
}

function main(): i32 {
  let root: string = name_of(0 as u32);
  let nobody: string = name_of(3999999999 as u32);
  print(root);
  print(nobody);
  if (root == "none" && nobody == "none") { return 0; }
  return 1;
}
`

// TestSelfHostGetpwuidName pins `__getpwuid_name` under the self-host. Linux
// and WASI keep every account in the files a caller reads, so the builtin
// answers 0 there and the program exits 0. An arm64-darwin image binds
// `_getpwuid` from libSystem, the slot its helper calls through.
func TestSelfHostGetpwuidName(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, getpwuidProgram, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (both lookups answer 0 here)\n%s", code, stderr)
			}
		})
	}
	t.Run("arm64-darwin-image", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "main.fern")
		if err := os.WriteFile(src, []byte(getpwuidProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "prog")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "arm64-darwin", src, cli.stdlib, "-o", out)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("self-host -target arm64-darwin: %v\n%s", err, msg)
		}
		img, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if !e2eharness.MachOBindsSymbol(img, "_getpwuid") {
			t.Fatalf("the image's LC_DYLD_INFO_ONLY bind stream does not name _getpwuid")
		}
	})
}

// TestSelfHostArm64DarwinGetpwuidName runs the same program as an Apple
// Silicon binary the self-host built natively: uid 0 must answer root, and a
// uid no account has must answer none.
func TestSelfHostArm64DarwinGetpwuidName(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "names.fern")
	if err := os.WriteFile(src, []byte(getpwuidProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "names")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	got, _ := exec.Command(bin).Output()
	if want := "root\nnone\n"; string(got) != want {
		t.Errorf("getpwuid answered %q, want %q", got, want)
	}
}
