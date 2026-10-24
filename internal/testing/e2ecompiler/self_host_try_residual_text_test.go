package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTryResidualNamesImportedTypes(t *testing.T) {
	h := selfHostCLIForHost(t)
	cases := []struct {
		name, src, want string
	}{
		{
			name: "dyn std trait, imported error type",
			src: `import "std/error";
import "custom";
function f(): Result[i32, dyn error.Error] { let v: i32 = custom.fail()?; return Ok(v); }
function main(): i32 { return 0; }
`,
			want: "error[E042]: `?` cannot box custom.NotError into dyn error.Error: custom.NotError does not implement error.Error",
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
			support := `pub struct NotError { code: i32 }
pub function fail(): Result[i32, NotError] { return Err(NotError { code: 1 }); }
`
			if err := os.WriteFile(filepath.Join(filepath.Dir(src), "custom.fern"), []byte(support), 0o644); err != nil {
				t.Fatal(err)
			}
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
