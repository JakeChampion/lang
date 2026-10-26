package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmTypeIDGlobalNeeds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name, source string
		ids          bool
	}{
		{"integer", `function main(): i32 { return 0; }`, false},
		{"string-view", `function main(): i32 {
  let text = args()[0] + " abc";
  let part: str = slice_unchecked(text, 0, text.len());
  if (part.len() != text.len()) { return 1; }
  return 0;
}`, false},
		{"dynamic-records", `trait Size { function size(self: Self): i32; }
struct Unused { n: i32 }
struct P { n: i32 }
struct Q { n: i32 }
impl Size for P { function size(self: P): i32 { return self.n; } }
impl Size for Q { function size(self: Q): i32 { return self.n + 2; } }
@noinline function make(flag: boolean): dyn Size {
  if (flag) { return P { n: 42 }; }
  return Q { n: 40 };
}
@noinline function size(x: dyn Size): i32 { return x.size(); }
function main(): i32 { return size(make(args().len() > 0)) - 42; }
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "main.fern")
			watPath := filepath.Join(dir, "main.wat")
			if err := os.WriteFile(source, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "asm", "-o", watPath, source, cli.stdlib)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("emit WAT: %v\n%s", err, output)
			}
			wat, err := os.ReadFile(watPath)
			if err != nil {
				t.Fatal(err)
			}
			ids := strings.Contains(string(wat), "(global $__tid$")
			if ids != tc.ids {
				t.Fatalf("type-ID globals present = %v, want %v", ids, tc.ids)
			}
			stderr, code := cli.exitOfFile(t, source, "wasm32-wasi", nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("runtime exit %d\n%s", code, stderr)
			}
			if tc.name == "integer" {
				var allocs, frees, live int64
				if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil {
					t.Fatal(err)
				}
				if allocs < 0 || allocs != frees || live != 0 {
					t.Fatalf("unbalanced integer census: %s", stderr)
				}
			} else {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}

func TestSelfHostWasmTypeIDRuntimeModes(t *testing.T) {
	validate, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const source = `import "./asmcore";
import "./lexer";
import "./parser";
import "./util";
import "./wasm_ir";
import "./semlower";
function emit(mode: i32, io: boolean): string {
  let mod = parser.parse_module(lexer.tokenize("function main(): i32 { return 0; }"));
  let text = wasm_ir.WasmUnit { ns: "", text: "  (func $main (result i32) (i32.const 0))\n", strs: [], caggs: [], fns: [] };
  let needs: string[] = if (io) { ["read_file", "@uses_strings", "@allocates"] } else { [] };
  return wasm_ir.emit_ir_module_units(wasm_ir.with_runtime_units([text], needs, semlower.runtime_module_lowering), mod, needs, [], mode, asmcore.env_switches());
}
function main(): i32 {
  for mode in [0, 1, 2] {
    print("=== case: plain-" + util.i32_to_string(mode));
    print(emit(mode, false));
  }
  for mode in [0, 2] {
    print("=== case: io-" + util.i32_to_string(mode));
    print(emit(mode, true));
  }
  return 0;
}
`
	entry := filepath.Join(dir, "type_id_modes.fern")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "type_id_modes.fern", "type-id-modes")
	output, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("mode driver: %v\n%s", err, output)
	}
	want := map[string]bool{"plain-0": false, "plain-1": false, "plain-2": false, "io-0": true, "io-2": true}
	for _, section := range strings.Split(string(output), "=== case: ")[1:] {
		name, wat, ok := strings.Cut(section, "\n")
		ids, exists := want[name]
		if !ok || !exists {
			t.Fatalf("unexpected mode section %q", name)
		}
		delete(want, name)
		t.Run(name, func(t *testing.T) {
			if strings.Contains(wat, "(global $__tid$") != ids {
				t.Fatalf("type-ID globals do not match mode %s", name)
			}
			path := filepath.Join(dir, name+".wat")
			if err := os.WriteFile(path, []byte(wat), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(validate, "validate", path).CombinedOutput(); err != nil {
				t.Fatalf("validate %s: %v\n%s", name, err, out)
			}
		})
	}
	if len(want) != 0 {
		t.Fatalf("missing mode sections: %v", want)
	}
}
