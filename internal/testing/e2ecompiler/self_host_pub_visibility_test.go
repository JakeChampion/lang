package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostPubVisibilityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	dir = t.TempDir()
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}

	lib := `pub function visible(): i32 { return 1; }
function hidden(): i32 { return 41; }
pub function uses_own_private(): i32 { return hidden() + 1; }
pub struct Open { a: i32 }
struct Secret { b: i32 }
pub enum Shown { One, Two(i32) }
enum Hidden { Alpha, Beta }
pub function hidden_node(): Hidden { return Alpha; }
pub function own_secret(): i32 { let s: Secret = Secret { b: 5 }; return s.b; }
@must_consume
struct Guarded { d: i32 }
@must_consume
pub struct GuardedOpen { d: i32 }
function eat_guarded(own g: Guarded): i32 { return g.d; }
pub function take_open(own g: GuardedOpen): i32 { return g.d; }
pub function own_guarded(): i32 { return eat_guarded(Guarded { d: 6 }); }
const HIDDEN_N: i32 = 8;
pub const SHOWN_N: i32 = 9;
pub(package) function pkg_helper(): i32 { return 7; }
pub(package) struct PkgBox { v: i32 }
pub(package) enum PkgNode { PkgEmpty, PkgLeaf(i32) }
`
	if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write lib.fern: %v", err)
	}

	check := func(t *testing.T, name, src string, wantExit int, wantMsg string) {
		t.Helper()
		path := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		cmd := exec.Command(fernBin, "-check", path, stdlibRoot)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		_ = cmd.Run()
		code := cmd.ProcessState.ExitCode()
		if code != wantExit {
			t.Errorf("%s: -check exit = %d, want %d\nstderr: %s", name, code, wantExit, errBuf.String())
		}
		if wantMsg != "" && !strings.Contains(errBuf.String(), wantMsg) {
			t.Errorf("%s: stderr missing %q\ngot: %s", name, wantMsg, errBuf.String())
		}
		if wantMsg == "" && (strings.Contains(errBuf.String(), "is not exported") || strings.Contains(errBuf.String(), "pub(package)")) {
			t.Errorf("%s: reported a visibility error on a legal program\ngot: %s", name, errBuf.String())
		}
	}

	check(t, "float-high", `import "std/f64"; function main(): i32 { let n = f64.pow10_hi(0); return 0; }`, 1, "pub(package)")

	check(t, "float-low", `import "std/f64"; function main(): i32 { let n = f64.pow10_lo(0); return 0; }`, 1, "pub(package)")

	check(t, "float-log", `import "std/f64"; function main(): i32 { let n = f64.pow10_log2(0); return 0; }`, 1, "pub(package)")

	check(t, "buffer-u64", `import "std/io_buffered"; function main(): i32 { io_buffered.buf_push_u64(0 as usize, 1u64); return 0; }`, 1, "pub(package)")

	check(t, "buffer-i64", `import "std/io_buffered"; function main(): i32 { io_buffered.buf_push_i64(0 as usize, 1i64); return 0; }`, 1, "pub(package)")

	check(t, "buffer-length", `import "std/io_buffered"; function main(): i32 { let n = io_buffered.buf_u64_len(1u64); return 0; }`, 1, "pub(package)")

	check(t, "digit-pairs", `import "std/io_buffered"; function main(): i32 { let digits = io_buffered.digit_pairs(); return 0; }`, 1, "pub(package)")

	check(t, "buffer-block", `import "std/io_buffered"; function inspect(b: io_buffered.BufBlock): void {} function main(): i32 { return 0; }`, 1, "pub(package)")

	check(t, "private_ref",
		"import \"./lib\";\nfunction main(): i32 { return lib.hidden(); }\n",
		1, "2:34: error[E087]: lib.hidden is not exported (declare it as `pub function hidden …` to make it accessible from other modules)")

	check(t, "private_ref_value_position",
		"import \"./lib\";\nfunction main(): i32 { let f: () => i32 = lib.hidden; return f(); }\n",
		1, "2:46: error[E087]: lib.hidden is not exported")

	check(t, "public_ref",
		"import \"./lib\";\nfunction main(): i32 { return lib.visible(); }\n",
		0, "")

	check(t, "private_enum_qualified_value",
		"import \"./lib\";\nfunction main(): i32 { let x = lib.Hidden.Alpha; return 0; }\n",
		1, "lib.Hidden is not exported")
	check(t, "private_enum_qualified_pattern",
		"import \"./lib\";\nfunction f(x: lib.Shown): i32 { match (x) { lib.Hidden.Alpha => { return 1; }, _ => { return 0; } } }\nfunction main(): i32 { return 0; }\n",
		1, "lib.Hidden is not exported")

	check(t, "own_private",
		"import \"./lib\";\nfunction main(): i32 { return lib.uses_own_private(); }\n",
		0, "")

	check(t, "private_const",
		"import \"./lib\";\nfunction main(): i32 { return lib.HIDDEN_N - 8; }\n",
		1, "lib.HIDDEN_N is not exported (declare it as `pub const HIDDEN_N …` to make it accessible from other modules)")

	check(t, "public_const",
		"import \"./lib\";\nfunction main(): i32 { return lib.SHOWN_N - 9; }\n",
		0, "")

	check(t, "private_type",
		"import \"./lib\";\nfunction main(): i32 { let s: lib.Secret = lib.Secret { b: 7 }; return s.b - 7; }\n",
		1, "lib.Secret is not exported (declare it as `pub struct Secret …` to make it accessible from other modules)")

	check(t, "public_type",
		"import \"./lib\";\nfunction main(): i32 { let s: lib.Open = lib.Open { a: 3 }; return s.a - 3; }\n",
		0, "")

	check(t, "private_enum",
		"import \"./lib\";\nfunction main(): i32 { let h: lib.Hidden = lib.Alpha; return 0; }\n",
		1, "is not exported (declare it as `pub enum Hidden …`")

	check(t, "public_enum_variant",
		"import \"./lib\";\nfunction main(): i32 { let s: lib.Shown = lib.Two(4); match (s) { Two(v) => { return v - 4; }, _ => { return 9; } } }\n",
		0, "")

	check(t, "own_private_type",
		"import \"./lib\";\nfunction main(): i32 { return lib.own_secret() - 5; }\n",
		0, "")

	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	subLib := `pub function visible(): i32 { return 2; }
function hidden(): i32 { return 42; }
`
	if err := os.WriteFile(filepath.Join(dir, "sub", "lib.fern"), []byte(subLib), 0o644); err != nil {
		t.Fatalf("write sub/lib.fern: %v", err)
	}

	check(t, "subdir_private_ref",
		"import \"./sub/lib\";\nfunction main(): i32 { return lib.hidden(); }\n",
		1, "lib.hidden is not exported")

	check(t, "subdir_public_ref",
		"import \"./sub/lib\";\nfunction main(): i32 { return lib.visible() - 2; }\n",
		0, "")

	check(t, "private_must_consume_type",
		"import \"./lib\";\nfunction peek(own g: lib.Guarded): i32 { return g.d; }\nfunction main(): i32 { return 0; }\n",
		1, "lib.Guarded is not exported (declare it as `pub struct Guarded …` to make it accessible from other modules)")

	check(t, "public_must_consume_type",
		"import \"./lib\";\nfunction main(): i32 { return lib.take_open(lib.GuardedOpen { d: 3 }) - 3; }\n",
		0, "")

	check(t, "own_private_must_consume_type",
		"import \"./lib\";\nfunction main(): i32 { return lib.own_guarded() - 6; }\n",
		0, "")

	// Without a manifest, package visibility follows the directory.

	if err := os.MkdirAll(filepath.Join(dir, "pkgsub"), 0o755); err != nil {
		t.Fatalf("mkdir pkgsub: %v", err)
	}
	for name, src := range map[string]string{
		"user.fern":      "import \"../lib\";\npub function call_pkg(): i32 { return lib.pkg_helper(); }\n",
		"user_type.fern": "import \"../lib\";\npub function make(): i32 { let b: lib.PkgBox = lib.PkgBox { v: 1 }; return b.v; }\n",
		"peer.fern":      "pub(package) function peer_helper(): i32 { return 3; }\n",
		"peer_user.fern": "import \"../pkgsub/peer\";\npub function via_dotdot(): i32 { return peer.peer_helper(); }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "pkgsub", name), []byte(src), 0o644); err != nil {
			t.Fatalf("write pkgsub/%s: %v", name, err)
		}
	}

	check(t, "pkg_same_dir",
		"import \"./lib\";\nfunction main(): i32 { return lib.pkg_helper() - 7; }\n",
		0, "")

	check(t, "pkg_type_same_dir",
		"import \"./lib\";\nfunction main(): i32 { let b: lib.PkgBox = lib.PkgBox { v: 2 }; return b.v - 2; }\n",
		0, "")

	check(t, "pkg_other_dir",
		"import \"./pkgsub/user\";\nfunction main(): i32 { return user.call_pkg(); }\n",
		1, "lib.pkg_helper is `pub(package)` — only modules in the same package as lib may use it")

	check(t, "pkg_type_other_dir",
		"import \"./pkgsub/user_type\";\nfunction main(): i32 { return user_type.make(); }\n",
		1, "lib.PkgBox is `pub(package)` — only modules in the same package as lib may use it")

	check(t, "pkg_dotdot_same_dir",
		"import \"./pkgsub/peer_user\";\nfunction main(): i32 { return peer_user.via_dotdot() - 3; }\n",
		0, "")

	check(t, "pkgsub/pkg_entry_other_dir",
		"import \"../lib\";\nfunction main(): i32 { return lib.pkg_helper(); }\n",
		1, "lib.pkg_helper is `pub(package)` — only modules in the same package as lib may use it")

	check(t, "private_variant_without_annotation",
		`import "./lib"; function main(): i32 { let node = lib.Alpha; return 0; }`,
		1, "is not exported")
	check(t, "private_bare_variant",
		`import "./lib"; function main(): i32 { let node = Alpha; return 0; }`,
		1, "not visible")
	check(t, "private_bare_pattern",
		`import "./lib"; function main(): i32 { let node = lib.hidden_node(); match (node) { Alpha => { return 0; }, Beta => { return 1; } } return 0; }`,
		1, "not visible")
	check(t, "package_variant_same_dir",
		`import "./lib"; function main(): i32 { let node = lib.PkgLeaf(7); match (node) { lib.PkgLeaf(n) => { return n; }, lib.PkgEmpty => { return 0; } } return 0; }`,
		0, "")
	check(t, "pkgsub/package_variant_other_dir",
		`import "../lib"; function main(): i32 { let node = lib.PkgLeaf(7); return 0; }`,
		1, "pub(package)")
	check(t, "package_bare_variant",
		`import "./lib"; function main(): i32 { let node = PkgLeaf(7); return 0; }`,
		0, "")
	check(t, "pkgsub/package_bare_variant_other_dir",
		`import "../lib"; function main(): i32 { let node = PkgLeaf(7); return 0; }`,
		1, "not visible")

	manifest := []byte("[package]\nname = \"app\"\n")
	if err := os.WriteFile(filepath.Join(dir, "fern.toml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	check(t, "manifest_subdir",
		`import "./pkgsub/user"; function main(): i32 { return user.call_pkg(); }`, 0, "")
	check(t, "manifest_type_subdir",
		`import "./pkgsub/user_type"; function main(): i32 { return user_type.make(); }`, 0, "")
	check(t, "pkgsub/manifest_entry_subdir",
		`import "../lib"; function main(): i32 { let node = lib.PkgLeaf(7); return 0; }`, 0, "")
	if err := os.WriteFile(filepath.Join(dir, "pkgsub", "fern.toml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	check(t, "nested_manifest_same_name",
		`import "./pkgsub/user"; function main(): i32 { return user.call_pkg(); }`, 1, "pub(package)")
	check(t, "pkgsub/nested_manifest_variant",
		`import "../lib"; function main(): i32 { let node = lib.PkgLeaf(7); return 0; }`, 1, "pub(package)")

	for name, source := range map[string]string{
		"utf8_scalar_helper": `import "core/utf8_scalar"; function main(): i32 { return utf8_scalar.step("a", 0, 1); }`,
		"integer_helper":     `import "core/int"; function main(): i32 { return int.__radix_digit(48); }`,
		"map_helper":         `import "core/map"; function main(): i32 { return map.__map_pow2_ceil(3); }`,
		"json_helper":        `import "std/json"; function main(): i32 { let x = json.from_json_field(json.JNull, "x"); return 0; }`,
		"node_type":          `import "std/pvec"; function inspect(n: pvec.VNode[i32]): i32 { return 0; } function main(): i32 { return 0; }`,
		"node_constructor":   `import "std/pvec"; function main(): i32 { let node = pvec.Leaf([1]); return 0; }`,
		"manual_decoder":     `import "std/json"; struct Item { value: i32 } impl json.FromJson for Item { function from_json_value(v: json.JsonValue): Result[Item, string] { let field = json.from_json_field(v, "value"); return Err("manual"); } } function main(): i32 { return 0; }`,
	} {
		check(t, "stdlib_"+name, source, 1, "pub(package)")
	}
	check(t, "generated_decoder",
		`import "std/json"; @derive(json.FromJson) struct Item { value: i32, names: string[], note: Option[string] } function main(): i32 { let item = Item.from_json("{\"value\":42,\"names\":[\"a\"]}"); return 0; }`, 0, "")
	if err := os.WriteFile(filepath.Join(dir, "shared.fern"), []byte(`enum Hidden { Shared(i32) } pub enum Open { Shared(string) }`), 0o644); err != nil {
		t.Fatal(err)
	}
	check(t, "public_variant_precedence",
		`import "./shared"; function main(): i32 { let node: shared.Open = shared.Shared("ok"); match (node) { shared.Shared(s) => { return s.len(); } } return 0; }`, 0, "")
	check(t, "entry_variant_does_not_hide_library_builtin",
		`import "std/json"; enum Reply { Ok, Full } function main(): i32 { let r: Reply = Reply.Ok; match (json.parse("1")) { Ok(_) => {}, Err(_) => { return 1; } } return match (r) { Ok => 0, Full => 2 }; }`, 0, "")

	src := "import \"./lib\";\nfunction main(): i32 { return lib.hidden(); }\n"
	path := filepath.Join(dir, "private_build.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := filepath.Join(dir, "private_build.bin")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, path)
	var buildErr strings.Builder
	cmd.Stderr = &buildErr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("compile of a private cross-module reference exited 0, want non-zero (native refuses to build it)")
	}
	if !strings.Contains(buildErr.String(), "is not exported") {
		t.Errorf("compile failed for the wrong reason — want the visibility diagnostic, got: %s", buildErr.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("compile emitted %s for a program native refuses to build", out)
	}
}
