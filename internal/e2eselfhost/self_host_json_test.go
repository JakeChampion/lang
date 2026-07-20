package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real std/json MODULE programs — `import "std/json";` with json.-qualified
// calls — compiled by the self-hosted compiler through the import-resolving
// loader driver (asm_load_run) with the repo's real stdlib as the root.
//
// This replaces the old harness that concatenated std/json's SOURCE with a
// main and fed it to the import-blind stdin driver (asm_run): that shape
// could never resolve std/json's own `import "std/string"`, so any
// cross-module method reference — json_get_f64 → s.parse_float() — emitted a
// call to `__fn_string__parse_float` with no definition and dangled at link
// (#5420). The loader path exercises the real import chain; the get-f64 case
// below is the regression guard for exactly that cross-module method shape.
//
// Exit codes are oracle-checked against the native interpreter per run, and
// pinned in `exit` so a stdlib semantic drift fails loudly rather than
// silently shifting both sides.
var jsonCases = []struct {
	name string
	main string
	exit int
}{
	{"encode-number", `var v: JsonValue = JNumber("42"); return json.json_encode(v).len();`, 2},
	{"encode-string", `var v: JsonValue = JString("hi"); return json.json_encode(v).len();`, 4},
	{"parse-object-ok", `match (json.json_parse("{\"a\":1}")) { Some(v) => { return 7; }, None => { return 0; } }`, 7},
	{"parse-bad", `match (json.json_parse("{bad")) { Some(v) => { return 1; }, None => { return 9; } }`, 9},
	{"get-i32", `match (json.json_parse("{\"n\":42}")) { Some(v) => { match (json.json_get_i32(v, "n")) { Some(x) => { return x; }, None => { return 0; } } }, None => { return 0; } } return 0;`, 42},
	{"parse-array", `match (json.json_parse("[1,2,3]")) { Some(v) => { return 7; }, None => { return 0; } }`, 7},
	{"nested-object", `match (json.json_parse("{\"x\":{\"y\":9}}")) { Some(v) => { match (json.json_get(v, "x")) { Some(inner) => { match (json.json_get_i32(inner, "y")) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }, None => { return 0; } } return 0;`, 9},
	// json_get_f64 routes through std/string's `s.parse_float()` receiver
	// method — the cross-module method reference #5420 tracked. Fraction,
	// exponent, and negative forms: 3.5*2 + 25 + 10 = 42.
	{"get-f64", `match (json.json_parse("{\"pi\":3.5,\"big\":2.5e1,\"neg\":-0.5}")) {
        Some(v) => {
            var r: i32 = 0;
            match (json.json_get_f64(v, "pi")) { Some(x) => { r = r + ((x * 2.0) as i32); }, None => { return 1; } }
            match (json.json_get_f64(v, "big")) { Some(x) => { r = r + (x as i32); }, None => { return 2; } }
            match (json.json_get_f64(v, "neg")) { Some(x) => { if (x < 0.0) { r = r + 10; } }, None => { return 3; } }
            return r;
        },
        None => { return 4; }
    } return 0;`, 42},
}

func jsonEntrySource(mainBody string) string {
	return "import \"std/json\";\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostJsonX86_64 compiles the std/json module programs with the
// self-hosted x86-64 loader driver and checks exit codes against the native
// interpreter oracle.
func TestSelfHostJsonX86_64(t *testing.T) {
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

	for _, tc := range jsonCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "json_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(jsonEntrySource(tc.main)), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if want != tc.exit {
				t.Fatalf("%s: native interp oracle = %d, want pinned %d — stdlib semantics drifted", tc.name, want, tc.exit)
			}
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "json_"+tc.name+"_bin", asm)
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

// TestSelfHostJsonArm64 — CI-gated arm64 counterpart: same loader driver
// (x86 host binary) emitting arm64 via -target, assembled + run under qemu.
func TestSelfHostJsonArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(x86runner[0], append(x86runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range jsonCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "json_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(jsonEntrySource(tc.main)), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if want != tc.exit {
				t.Fatalf("%s: native interp oracle = %d, want pinned %d — stdlib semantics drifted", tc.name, want, tc.exit)
			}
			asm, _ := runDriver(entry, root, "-target", "arm64")
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, arm64gcc, dir, "json_"+tc.name+"_arm64_bin", asm)
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host arm64 run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
