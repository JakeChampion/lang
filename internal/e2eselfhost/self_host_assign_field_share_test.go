package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A string stored into a struct by ASSIGNMENT (`r = Rec { name: s }`), or read
// back out of a field into a string local (`keep = r.name`,
// `var t: string = r.name`), gives its box back on the AST lowering as it does
// on the semantic one (#10371). The holder's superseded box releases the
// field, the reading local holds a counted share it releases itself, and a
// reassign of the box the slot already holds gives its retain back.
// Every answer is interpreter-confirmed.
var assignFieldShareCases = []struct {
	name string
	src  string
	want int
}{
	{"holder_assign", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var r: Rec = Rec { name: "start" };
    while (i < n) {
        var s: string = (i + 1000).to_string();
        r = Rec { name: s };
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (r.name != "1199") { return 97; }
    return (acc + r.name.len()) % 251;
}
function main(): i32 { return run(200); }
`, 51},
	{"holder_assign_concat", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var r: Rec = Rec { name: "start" };
    while (i < n) {
        var s: string = "12" + (i + 1000).to_string();
        r = Rec { name: s };
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (r.name != "121199") { return 97; }
    return (acc + r.name.len()) % 97;
}
function main(): i32 { return run(200); }
`, 8},
	{"field_alias", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: string = "start";
    while (i < n) {
        var s: string = (i + 1000).to_string();
        var r: Rec = Rec { name: s };
        keep = r.name;
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (keep != "1199") { return 97; }
    return (acc + keep.len()) % 251;
}
function main(): i32 { return run(200); }
`, 51},
	{"holder_and_alias", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: string = "start";
    var r: Rec = Rec { name: "none" };
    while (i < n) {
        var s: string = (i + 1000).to_string();
        r = Rec { name: s };
        keep = r.name;
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (keep != "1199" || r.name != "1199") { return 97; }
    return (acc + keep.len() + r.name.len()) % 101;
}
function main(): i32 { return run(200); }
`, 55},
	{"field_alias_branch", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: string = "start";
    while (i < n) {
        var s: string = "12" + (i + 1000).to_string();
        var r: Rec = Rec { name: s };
        if (i % 3 == 0) { keep = r.name; }
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (keep != "121198") { return 97; }
    return (acc + keep.len()) % 97;
}
function main(): i32 { return run(200); }
`, 8},
	{"field_alias_same_box", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: string = "start";
    while (i < n) {
        var s: string = (i + 1000).to_string();
        var r: Rec = Rec { name: s };
        keep = r.name;
        keep = r.name;
        acc = (acc + keep.len()) % 251;
        i = i + 1;
    }
    return (acc + keep.len()) % 251;
}
function main(): i32 { return run(200); }
`, 51},
	{"alias_same_box", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var prev: string = "start";
    while (i < n) {
        var s: string = (i + 1000).to_string();
        prev = s;
        prev = s;
        acc = (acc + prev.len()) % 251;
        i = i + 1;
    }
    return (acc + prev.len()) % 251;
}
function main(): i32 { return run(200); }
`, 51},
	{"field_bind", `import "std/i32";
import "std/string";
struct Rec { name: string }
function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var r: Rec = Rec { name: "start" };
    while (i < n) {
        var s: string = (i + 1000).to_string();
        r = Rec { name: s };
        var t: string = r.name;
        acc = (acc + t.len() + s.len()) % 251;
        i = i + 1;
    }
    return (acc + r.name.len()) % 251;
}
function main(): i32 { return run(200); }
`, 98},
}

// The shapes sit in run(), so skipping run or main mixes the lowerings.
var assignFieldShareLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
	{"ast_main", "FERN_SEM_IR_SKIP=main"},
	{"ast_run", "FERN_SEM_IR_SKIP=run"},
}

func writeAssignFieldShareSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostAssignFieldShareX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range assignFieldShareCases {
		src := writeAssignFieldShareSrc(t, tc.name, tc.src)
		for _, lw := range assignFieldShareLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env), nil)
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
				stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env), nil)
				if exit != tc.want || strings.Contains(stderr, "fern-sanitizer:") {
					t.Fatalf("FERN_SANITIZE: exit = %d, want %d, and the sanitizer silent\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostAssignFieldShareArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range assignFieldShareCases {
		src := writeAssignFieldShareSrc(t, tc.name, tc.src)
		for _, lw := range assignFieldShareLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", lw.env))
				if err != nil {
					t.Fatal(err)
				}
				cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), tc.name, string(asm)))
				var eb strings.Builder
				cmd.Stderr = &eb
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", code, tc.want, eb.String())
				}
				assertBalancedCensus(t, eb.String())
			})
		}
	}
}

func TestSelfHostAssignFieldShareWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range assignFieldShareCases {
		src := writeAssignFieldShareSrc(t, tc.name, tc.src)
		for _, lw := range assignFieldShareLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env))
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
