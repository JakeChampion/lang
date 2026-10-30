package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// reproRoot is one build's inputs: a source root holding main.fern and its
// own copy of the stdlib, a working directory, and an environment.
type reproRoot struct {
	src, stdlib, work string
	env               []string
}

func newReproRoot(t *testing.T, stdlibSrc, root string, depth int, env ...string) reproRoot {
	t.Helper()
	src := filepath.Join(root, "src")
	work := filepath.Join(root, "work")
	for i := 0; i < depth; i++ {
		work = filepath.Join(work, "deeper")
	}
	stdlib := filepath.Join(root, "lib", "stdlib")
	for _, d := range []string{src, work, filepath.Dir(stdlib)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(stdlib, os.DirFS(stdlibSrc)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.fern"), []byte(reproSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return reproRoot{src: src, stdlib: stdlib, work: work, env: env}
}

func (r reproRoot) cmd(cli *selfHostCLI, out string, flags ...string) *exec.Cmd {
	args := append(append([]string{}, flags...), "-o", out, filepath.Join(r.src, "main.fern"), r.stdlib)
	cmd := runX86_64Bin(cli.runner, cli.bin, args...)
	cmd.Dir = r.work
	cmd.Env = childEnv(r.env...)
	return cmd
}

func (r reproRoot) build(t *testing.T, cli *selfHostCLI, flags ...string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	if msg, err := r.cmd(cli, out, flags...).CombinedOutput(); err != nil {
		t.Fatalf("compile in %s: %v\n%s", r.work, err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type reproVariant struct {
	name  string
	flags []string
}

// remap maps the source root and the working directory to fixed names.
func (r reproRoot) remap() []string {
	return []string{"-remap-path-prefix", filepath.Dir(r.src) + "=/src", "-remap-path-prefix=" + r.work + "=/build"}
}

// TestSelfHostReproducibleAcrossPaths compiles one program from two source
// roots, two stdlib roots and two working directories, under two locales,
// two time zones and two SOURCE_DATE_EPOCH values, and requires
// byte-identical images on every target that writes one. Each compile is its
// own process, so each also runs under a different string-map hash seed. A -g
// build carries paths: it must differ without -remap-path-prefix and match
// with it.
func TestSelfHostReproducibleAcrossPaths(t *testing.T) {
	cli := buildSelfHostCLI(t)
	a := newReproRoot(t, cli.stdlib, filepath.Join(t.TempDir(), "first-place"), 0,
		"HOME=/nonexistent/home-a", "USER=alpha", "LANG=C", "LC_ALL=C", "TZ=UTC", "SOURCE_DATE_EPOCH=0", "FERN_REPRO_PROBE=a")
	b := newReproRoot(t, cli.stdlib, filepath.Join(t.TempDir(), "elsewhere", "x"), 2,
		"HOME=/nonexistent/home-b", "USER=beta", "LANG=tr_TR.UTF-8", "LC_ALL=tr_TR.UTF-8", "TZ=Asia/Tokyo", "SOURCE_DATE_EPOCH=2000000000", "FERN_REPRO_PROBE=b")
	same := func(t *testing.T, what string, x, y []byte) {
		t.Helper()
		if !bytes.Equal(x, y) {
			t.Errorf("%s: the two images differ (%d and %d bytes)", what, len(x), len(y))
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			variants := []reproVariant{{"plain", nil}}
			if strings.HasSuffix(target, "-linux") {
				variants = append(variants, reproVariant{"pie", []string{"-pie"}}, reproVariant{"harden", []string{"-harden"}})
			}
			for _, v := range variants {
				t.Run(v.name, func(t *testing.T) {
					flags := append([]string{"-target", target}, v.flags...)
					same(t, target+" "+v.name, a.build(t, cli, flags...), b.build(t, cli, flags...))
				})
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		t.Run(target+"/debug-info", func(t *testing.T) {
			flags := []string{"-target", target, "-g"}
			if bytes.Equal(a.build(t, cli, flags...), b.build(t, cli, flags...)) {
				t.Errorf("%s -g: the images match without -remap-path-prefix, so the test no longer sees the paths it remaps", target)
			}
			x := a.build(t, cli, append(flags, a.remap()...)...)
			y := b.build(t, cli, append(flags, b.remap()...)...)
			same(t, target+" -g -remap-path-prefix", x, y)
			for _, root := range []string{a.src, a.work} {
				if bytes.Contains(x, []byte(root)) {
					t.Errorf("%s -g -remap-path-prefix: the image still holds %s", target, root)
				}
			}
		})
	}
}

// TestSelfHostRemapPathPrefix pins how -remap-path-prefix reads its value
// and which prefix it replaces, through the -emit asm file table.
func TestSelfHostRemapPathPrefix(t *testing.T) {
	cli := buildSelfHostCLI(t)
	r := newReproRoot(t, cli.stdlib, filepath.Join(t.TempDir(), "root"), 0)
	entry := func(t *testing.T, remaps ...string) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "out.s")
		flags := append([]string{"-target", "x86-64-linux", "-g", "-emit", "asm"}, remaps...)
		if msg, err := r.cmd(cli, out, flags...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(asm), "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), `.file 1 "`); ok {
				return strings.TrimSuffix(rest, `"`)
			}
		}
		t.Fatalf("no .file 1 line in the -g assembly")
		return ""
	}
	root := filepath.Dir(r.src)
	for _, tc := range []struct {
		name   string
		remaps []string
		want   string
	}{
		{"none", nil, filepath.Join(r.src, "main.fern")},
		{"directory", []string{"-remap-path-prefix", root + "=/src"}, "/src/src/main.fern"},
		{"trailing-slash", []string{"-remap-path-prefix", root + "/=/src/"}, "/src/src/main.fern"},
		{"empty-to", []string{"-remap-path-prefix", r.src + "="}, "main.fern"},
		{"whole-path", []string{"-remap-path-prefix", filepath.Join(r.src, "main.fern") + "=entry.fern"}, "entry.fern"},
		{"partial-component", []string{"-remap-path-prefix", r.src[:len(r.src)-1] + "=/no"}, filepath.Join(r.src, "main.fern")},
		{"last-match-wins", []string{"-remap-path-prefix", r.src + "=/inner", "-remap-path-prefix", root + "=/outer"}, "/outer/src/main.fern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := entry(t, tc.remaps...); got != tc.want {
				t.Errorf("file 1 is %q, want %q", got, tc.want)
			}
		})
	}
	for _, tc := range []struct{ value, want string }{
		{"nothing", `-remap-path-prefix takes FROM=TO with a non-empty FROM (got "nothing")`},
		{"=/x", `-remap-path-prefix takes FROM=TO with a non-empty FROM (got "=/x")`},
	} {
		t.Run("refuses/"+tc.value, func(t *testing.T) {
			cmd := r.cmd(cli, filepath.Join(t.TempDir(), "out"), "-target", "x86-64-linux", "-g", "-remap-path-prefix", tc.value)
			msg, err := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); err == nil || code != 2 {
				t.Fatalf("exit %d, want 2\n%s", code, msg)
			}
			if !strings.Contains(string(msg), tc.want) {
				t.Errorf("stderr %q does not hold %q", msg, tc.want)
			}
		})
	}
}
