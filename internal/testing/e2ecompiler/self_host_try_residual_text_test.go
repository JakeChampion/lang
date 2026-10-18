package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// E042 for a `?` whose error type does not convert names the missing impl,
// with an imported type spelled as the reader writes it rather than as the
// module loader mangles it (#12668). The hint-text differential's driver
// loads no imports, so these rows run both CLIs over the real closure.
func TestSelfHostTryResidualNamesImportedTypes(t *testing.T) {
	h := selfHostCLIForHost(t)
	cases := []struct {
		name, src, want string
	}{
		{
			name: "dyn std trait, imported error type",
			src: `import "std/error";
import "std/brotli";
function f(b: u8[]): Result[i32, dyn error.Error] { let v: u8[] = brotli.decompress(b, 16)?; return Ok(v.len()); }
function main(): i32 { return 0; }
`,
			want: "error[E042]: `?` cannot box brotli.BrotliError into dyn error.Error: brotli.BrotliError does not implement error.Error",
		},
		{
			name: "concrete imported error type",
			src: `import "std/brotli";
struct Mine { code: i32 }
function f(b: u8[]): Result[i32, Mine] { let v: u8[] = brotli.decompress(b, 16)?; return Ok(v.len()); }
function main(): i32 { return 0; }
`,
			want: "error[E042]: `?` on Result carrying brotli.BrotliError but the surrounding function carries Mine, and Mine has no `from(brotli.BrotliError)` constructor to convert it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, run := range []struct {
				who  string
				args []string
			}{
				{"self-host", []string{h.cli, "-check", "-color", "never", src, h.stdlib}},
				{"native", []string{h.native, "-check", "-color", "never", src}},
			} {
				out, _ := exec.Command(run.args[0], run.args[1:]...).CombinedOutput()
				if !strings.Contains(string(out), tc.want) {
					t.Errorf("%s does not say %q:\n%s", run.who, tc.want, out)
				}
			}
		})
	}
}
