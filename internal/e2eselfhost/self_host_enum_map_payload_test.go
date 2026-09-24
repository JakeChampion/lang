package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// One Map-typed variant payload refused the whole enum's release on the AST
// lowering: enum_field_rc_droppable had no row for a map, so every variant's
// payloads leaked with it, the Map variant never having been built (#9304). A
// map payload is now admitted and released by nothing, since a map box has no
// count to say whether the enum is its only holder.
const enumMapDeclaredSrc = `import "core/map";
enum Rec { Text(string), Pair(string, string), Obj(Map[string, string]) }

function once(v: string): i32 {
    var r: Rec = Pair(v + "-a", v + "-b-longer-payload");
    match (r) {
        Pair(a, b) => { return a.len() + b.len(); },
        Text(s) => { return s.len(); },
        Obj(m) => { return 0; }
    }
    return 0;
}
function main(): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < 200) { n = once("val"); i = i + 1; }
    return n;
}
`

// The Map variant constructed on every third round. Its maps still leak, so
// this row asserts the answer and that nothing is released twice.
const enumMapBuiltSrc = `import "core/map";
enum Rec { Text(string), Pair(string, string), Obj(Map[string, string]) }
function once(v: string, k: i32): i32 {
    var r: Rec = Pair(v + "-a", v + "-b-longer-payload");
    if (k % 3 == 0) {
        var m: Map[string, string] = map_new(4);
        m = m.insert("key", v + "-value-longer-than-inline");
        r = Obj(m);
    }
    match (r) {
        Pair(a, b) => { return a.len() + b.len(); },
        Text(s) => { return s.len(); },
        Obj(mm) => { return mm.len(); }
    }
    return 0;
}
function main(): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < 200) { n = n + once("val", i); i = i + 1; }
    return n % 101;
}
`

// A recursive enum with a Map variant: the payload array's elements are
// themselves enum boxes, released through the array.
const enumMapRecursiveSrc = `import "core/map";
enum Tree3 { Leaf(string), Node(Tree3[]), Obj(Map[string, Tree3]) }
function once(v: string): i32 {
    var r: Tree3 = Node([Leaf(v + "-a"), Leaf(v + "-b-longer-payload")]);
    match (r) {
        Node(ks) => { return ks.len(); },
        Leaf(s) => { return s.len(); },
        Obj(m) => { return 0; }
    }
    return 0;
}
function main(): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < 200) { n = once("val"); i = i + 1; }
    return n;
}
`

// The typed lowering releases the built maps and the recursive payloads as
// well; the AST lowering still strands both.
func TestSelfHostEnumMapPayloadTypedReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, sc := range []struct {
		name, src string
		want      int
	}{
		{"built", enumMapBuiltSrc, 59},
		{"recursive", enumMapRecursiveSrc, 2},
	} {
		src := writeEnumMapSrc(t, "enum_map_"+sc.name, sc.src)
		t.Run(sc.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SEM_IR=1")
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != sc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, sc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

var enumMapLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
}

func writeEnumMapSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostEnumMapPayloadReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeEnumMapSrc(t, "enum_map_declared", enumMapDeclaredSrc)
	for _, lw := range enumMapLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != 25 {
				t.Fatalf("exit = %d, want 25\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostEnumMapPayloadSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	srcs := []struct {
		name, src string
		want      int
	}{
		{"declared", enumMapDeclaredSrc, 25},
		{"built", enumMapBuiltSrc, 59},
	}
	for _, sc := range srcs {
		src := writeEnumMapSrc(t, "enum_map_"+sc.name, sc.src)
		for _, lw := range enumMapLowerings {
			t.Run(sc.name+"/"+lw.name, func(t *testing.T) {
				bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env)
				stderr, exit := runWithStdin(t, cli.runner, bin, nil)
				if exit != sc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, sc.want, stderr)
				}
				for _, bad := range []string{"use-after-free", "double free", "over-release", "underflow"} {
					if strings.Contains(stderr, bad) {
						t.Fatalf("sanitizer reports %s\n%s", bad, stderr)
					}
				}
			})
		}
	}
}

func TestSelfHostEnumMapPayloadReleaseArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeEnumMapSrc(t, "enum_map_declared", enumMapDeclaredSrc)
	for _, lw := range enumMapLowerings {
		t.Run(lw.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", lw.env))
			if err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "enum_map", string(asm)))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 25 {
				t.Fatalf("exit = %d, want 25\n%s", code, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostEnumMapPayloadReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum map payload")
	}
	cli := buildSelfHostCLI(t)
	src := writeEnumMapSrc(t, "enum_map_declared", enumMapDeclaredSrc)
	for _, lw := range enumMapLowerings {
		t.Run(lw.name, func(t *testing.T) {
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWasmCensus(t, wat)
			if exit != 25 {
				t.Fatalf("exit = %d, want 25\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
