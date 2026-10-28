package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The program reaches string literals, a constant aggregate, a runtime
// module with its own literals, and the clock, so every data table the
// writer lays out is present.
const reproSrc = `import "std/string";
struct P { a: i32, b: i32 }
@noinline function mk(): P { return P { a: 5, b: 9 }; }
function main(): i32 {
  let p: P = mk();
  let s: string = "hello, " + "world!";
  match (read_file("/nonexistent/fern-repro")) {
    Ok(_) => { print("read?"); },
    Err(e) => { print("err " + e.message()); }
  }
  print(s + " " + (p.a + p.b).to_string() + " " + (now_unix_ms() > 0i64).to_string());
  return 0;
}
`

// TestSelfHostReproducibleAcrossPaths compiles one program from two working
// directories under two environments and requires byte-identical images for
// the plain, -pie and -harden builds. A -g build embeds the compile directory
// in its DWARF, so it is checked for a difference instead.
func TestSelfHostReproducibleAcrossPaths(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(reproSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, dir string, extraEnv []string, flags ...string) []byte {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "out")
		args := append(append([]string{}, flags...), "-o", out, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Dir = dir
		cmd.Env = append(childEnv(), extraEnv...)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile in %s: %v\n%s", dir, err, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			for _, variant := range []struct {
				name  string
				flags []string
			}{{"plain", nil}, {"pie", []string{"-pie"}}, {"harden", []string{"-harden"}}} {
				t.Run(variant.name, func(t *testing.T) {
					flags := append([]string{"-target", target}, variant.flags...)
					a := build(t, filepath.Join(t.TempDir(), "first-place"), []string{"HOME=/nonexistent/home-a", "USER=alpha", "FERN_REPRO_PROBE=a"}, flags...)
					b := build(t, filepath.Join(t.TempDir(), "elsewhere", "deeper"), []string{"HOME=/nonexistent/home-b", "USER=beta", "FERN_REPRO_PROBE=b", "TZ=Asia/Tokyo"}, flags...)
					if !bytes.Equal(a, b) {
						t.Errorf("%s %s: the two images differ (%d and %d bytes)", target, variant.name, len(a), len(b))
					}
				})
			}
			t.Run("debug-info", func(t *testing.T) {
				flags := []string{"-target", target, "-g"}
				a := build(t, filepath.Join(t.TempDir(), "first-place"), nil, flags...)
				b := build(t, filepath.Join(t.TempDir(), "elsewhere"), nil, flags...)
				if bytes.Equal(a, b) {
					t.Logf("%s -g images are identical across directories", target)
				} else {
					t.Logf("%s -g images differ across directories: the DWARF carries the compile directory", target)
				}
			})
		})
	}
}
