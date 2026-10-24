package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var traitDefaultHijackProject = map[string]string{
	"lib.fern": `pub function secret_helper(): i32 { return 41; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return secret_helper() + 1; }
}`,
	"main.fern": `import "./lib";
function secret_helper(): i32 { return 900; }
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }`,
}

var traitDefaultPrivateHelperProject = map[string]string{
	"lib.fern": `function private_helper(): i32 { return 41; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return private_helper() + 1; }
}`,
	"main.fern": `import "./lib";
function private_helper(): i32 { return 900; }
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.greet(); }`,
}

var traitDefaultGenericProject = map[string]string{
	"deep.fern": `pub function d(): i32 { return 41; }`,
	"lib.fern": `import "./deep";
pub function base(): i32 { return 40; }
pub struct Wrap { w: i32 }
pub trait Conv[T] {
    function seed(self: Self): T;
    function one(self: Self): i32 { return base() + 1; }
    function two(self: Self): i32 { return self.one() + 1; }
    function boxed(self: Self): i32 { let b: Wrap = Wrap { w: base() }; return b.w; }
    function far(self: Self): i32 { return deep.d(); }
}`,
	"main.fern": `import "./lib";
function base(): i32 { return 900; }
struct Wrap { w: i32 }
struct R { n: i32 }
impl lib.Conv[i32] for R {
    function seed(self: Self): i32 { return self.n; }
}
function main(): i32 {
    let r: R = R { n: 1 };
    return r.one() + r.two() + r.boxed() + r.far();
}`,
}

var traitDefaultDynProject = map[string]string{
	"lib.fern": `pub function base(): i32 { return 41; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return base() + self.tag(); }
}`,
	"main.fern": `import "./lib";
function base(): i32 { return 900; }
struct R { n: i32 }
impl lib.Greet for R {
    function tag(self: Self): i32 { return self.n; }
}
function call(g: dyn lib.Greet): i32 { return g.greet(); }
function main(): i32 { return call(R { n: 1 }); }`,
}

var traitDefaultSameModuleProject = map[string]string{
	"lib.fern": `pub function h(): i32 { return 41; }
pub struct S { v: i32 }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { return h() + 1; }
}
impl Greet for S { function tag(self: Self): i32 { return self.v; } }`,
	"main.fern": `import "./lib";
function main(): i32 { let s: lib.S = lib.S { v: 1 }; return s.greet(); }`,
}

var traitDefaultParametricImplProject = map[string]string{
	"lib.fern": `pub function base(): i32 { return 41; }
pub trait Sized2 {
    function size(self: Self): i32;
    function padded(self: Self): i32 { return self.size() + base(); }
}`,
	"main.fern": `import "./lib";
function base(): i32 { return 900; }
struct Box[T] { v: T }
impl[T] lib.Sized2 for Box[T] {
    function size(self: Self): i32 { return 1; }
}
function main(): i32 { let b: Box[i32] = Box[i32] { v: 7 }; return b.padded(); }`,
}

var traitDefaultProjects = []struct {
	name  string
	files map[string]string
	want  int
}{
	{"implementer shadows the helper", traitDefaultHijackProject, 42},
	{"helper is private to the trait's module", traitDefaultPrivateHelperProject, 42},
	{"generic trait, chained defaults, transitive import", traitDefaultGenericProject, 164},
	{"trait implemented in its own module", traitDefaultSameModuleProject, 42},
	{"inherited default reached through a dyn", traitDefaultDynProject, 42},
	{"parametric impl inherits the default", traitDefaultParametricImplProject, 42},
}

func TestInterpTraitDefaultBodyResolvesInDeclaringModule(t *testing.T) {
	bin := buildLangBinForInterp(t)
	for _, tc := range traitDefaultProjects {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, tc.files)
			cmd := exec.Command(bin, "-interp", filepath.Join(dir, "main.fern"))
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tc.want, out.String(), errb.String())
			}
		})
	}
}

func TestX86_64TraitDefaultBodyResolvesInDeclaringModule(t *testing.T) {
	for _, tc := range traitDefaultProjects {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, tc.files)
			if _, code := runFixtureX86_64(t, filepath.Join(dir, "main.fern"), ""); code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestCapabilitiesChargeTraitDefaultToDeclaringPackage(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for name, src := range map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nb = { path = \"../b\", capabilities = [] }\n",
		"app/main.fern": `import "b";
import "std/string";
struct R { n: i32 }
impl b.Leaky for R {
    function tag(self: Self): i32 { return self.n; }
}
function main(): i32 { let r: R = R { n: 1 }; return r.grab().len(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern": `pub trait Leaky {
    function tag(self: Self): i32;
    function grab(self: Self): string {
        match (read_file("/etc/hostname")) {
            Ok(s) => { return s; },
            Err(e) => { return "err"; },
        }
    }
}`,
	} {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "-capabilities", filepath.Join(dir, "app", "main.fern"))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("-capabilities: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}
	var bRow string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "b ") {
			bRow = line
		}
	}
	if bRow == "" {
		t.Fatalf("package b must be charged for the fs its own trait default reaches; report:\n%s", out.String())
	}
	if !strings.Contains(bRow, "fs") || !strings.Contains(bRow, "read_file") {
		t.Errorf("b's row should name fs via read_file, got %q", bRow)
	}
}
