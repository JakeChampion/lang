package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wasmNeedLinkDriver emits, for every op need the wasm backend recognises
// (wasm_ir.need_ops), a preview1 command module whose needs are what
// needs_of records for a unit holding that one op and nothing else, over a
// unit text holding only an empty main. Each module is printed behind a
// `=== need: <op>` line for the test to validate on its own; an op needs_of
// does not record for its own unit is reported behind `=== unrecorded:`.
const wasmNeedLinkDriver = `import "./ir";
import "./irtables";
import "./lexer";
import "./parser";
import "./util";
import "./wasm_ir";

function unit(o: ir.Op): irtables.LowerResult[] {
    return [irtables.LowerResult { ok: true, why: "", ops: [o], n_locals: 0,
        n_params: 0, erased_wide: false, superseded: false, arr_slots: [], i64_slots: [],
        f64_slots: [], str_slots: [], alias_incs: [], name: "", result_kind: irtables.result_i32() }];
}

function main(): i32 {
    let mod: parser.Module = parser.parse_module(lexer.tokenize("function main(): i32 { return 0; }"));
    let text: wasm_ir.WasmUnit = wasm_ir.WasmUnit { ns: "", text: "  (func $main (result i32) (i32.const 0))\n", strs: [], caggs: [], fns: [] };
    for op in wasm_ir.need_ops() {
        let o: ir.Op = ir.Op { decl: 0 - 1, kind_tag: ir.kind_id(op), i32_imm: 0, i64_imm: 0, f64_imm: 0.0, width: 0, unsigned: false, str: "" };
        let needs: string[] = wasm_ir.needs_of(unit(o));
        if (util.index_of_str(needs, op) < 0) {
            print("=== unrecorded: " + op);
            needs = needs.append(op);
        }
        print("=== need: " + op);
        print(wasm_ir.emit_ir_module_units([text], mod, needs, [], "", 0));
    }
    return 0;
}
`

// TestSelfHostWasmEveryNeedLinksAlone is the completeness test for the
// wasm emit's need gates (#10856): a module that needs exactly one op must
// validate, so a helper that calls another helper or an import its own need
// does not gate fails here, naming the op, rather than as an unknown
// function in whichever program first reaches that op on its own.
// $__fern_build_io_error's gate, needs_io_error, is a hand-kept list of the
// fs ops and had drifted twice before this test existed.
func TestSelfHostWasmEveryNeedLinksAlone(t *testing.T) {
	validate, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "fnsigs.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "need_link.fern"), []byte(wasmNeedLinkDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "need_link.fern", "need_link")
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("need driver: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if op, ok := strings.CutPrefix(line, "=== unrecorded: "); ok {
			t.Errorf("needs_of does not record %q for a unit holding only that op", op)
		}
	}
	sections := strings.Split(string(out), "=== need: ")
	if len(sections) < 2 {
		t.Fatalf("the driver printed no module:\n%s", out)
	}
	n := 0
	for _, sec := range sections[1:] {
		op, wat, _ := strings.Cut(sec, "\n")
		path := filepath.Join(dir, op+".wat")
		if err := os.WriteFile(path, []byte(wat), 0o644); err != nil {
			t.Fatal(err)
		}
		if msg, err := exec.Command(validate, "validate", path).CombinedOutput(); err != nil {
			t.Errorf("a module needing only %q does not validate: %v\n%s", op, err, msg)
		}
		n++
	}
	t.Logf("%d needs validated", n)
}
