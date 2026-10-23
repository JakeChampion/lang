package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostRuntimeUnitRegisterCalls(t *testing.T) {
	cli := newStrictCLI(t)
	path := strconv.Quote(filepath.Join(t.TempDir(), "missing"))
	src := `function main(): i32 {
  match (read_file(` + path + `)) {
    Ok(_) => { return 1; },
    Err(e) => {
      match (e) {
        NotFound(path) => { if (path == ` + path + `) { return 0; } return 2; },
        _ => { return 3; }
      }
    }
  }
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		t.Run(target, func(t *testing.T) {
			asm := cli.emit(t, target, src)
			for _, helper := range []string{"path_copy", "io_error", "utf8_valid"} {
				call := regexp.MustCompile(`(?m)^\s+(?:call|bl|jmp|b) L?__fn___fern_` + helper + `\.r\s*$`)
				if !call.MatchString(asm) {
					t.Errorf("runtime call to %s does not use its register entry", helper)
				}
			}
			switch target {
			case "x86-64-linux":
				if code, out := cli.runX86(t, asm); code != 0 {
					t.Fatalf("missing-file result: exit %d\n%s", code, out)
				}
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				if code, out := runArm64(t, gcc, qemu, asm); code != 0 {
					t.Fatalf("missing-file result: exit %d\n%s", code, out)
				}
			}
		})
	}
	asm := cli.emit(t, "x86-64-linux", src, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if code, out := cli.runX86(t, asm); code != 0 {
		t.Fatalf("instrumented missing-file result: exit %d\n%s", code, out)
	}
}

func TestSelfHostRuntimeUnitMetadataScope(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	const src = `import "./asmcore";
import "./asm_arm64_ir";
import "./irtables";
import "./semlower";
import "./ssa";
function main(): i32 {
  let old: asmcore.EmitState = asmcore.new_state();
  let s: asmcore.EmitState = asmcore.EmitState { ...old,
    rt_lower: semlower.runtime_lowering,
    leaves: ssa.leaves_of([ssa.Leaf { name: "outside", func: ssa.SFunc { name: "outside", nparams: 0, nvals: 0, blocks: [], entry: 0, takes_env: false } }]),
    reg_entries: irtables.sig_reg_of(["outside"]),
    float_abi: irtables.sig_reg_of(["outside"]), float_abi_masks: ["f|f"] };
  let source: string = "@noinline function callee(x: f64): f64 { return x + 1.5; }\n"
    + "@noinline function caller(x: f64): f64 { return callee(x) * 2.0; }\n";
  let out: asmcore.EmitState = asm_arm64_ir.emit_ir_runtime_fern_fn(source, s);
  if (irtables.sig_reg_index(out.reg_entries, "outside") < 0) { return 1; }
  if (irtables.sig_reg_index(out.reg_entries, "callee") >= 0) { return 2; }
  if (out.float_mask("outside") != "f|f" || out.float_mask("callee") != "") { return 3; }
  if (out.leaves.list.len() != 1 || out.leaves.list[0].name != "outside") { return 4; }
  write(out.take_text());
  out.release();
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "runtime_scope.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "runtime_scope.fern", "runtime_scope")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("runtime unit metadata: %v\n%s", err, out)
	}
	asm := string(out)
	if !strings.Contains(asm, "bl __fn_callee.r\n") || !strings.Contains(asm, "__fn_callee.k:\n") {
		t.Fatal("floating-point runtime call has no register ABI")
	}
}

func TestSelfHostRuntimeUnitByteCopyMoves(t *testing.T) {
	cli := newStrictCLI(t)
	stackCopy := regexp.MustCompile(`(?m)(?:    (?:pushq|popq) %[^\n]+\n){2,}    rep movsb\n`)
	for _, name := range []string{"byte_copy_append", "byte_copy_with"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(langSrcAbs(t, "bench/"+name+".fern"))
			if err != nil {
				t.Fatal(err)
			}
			asm := cli.emit(t, "x86-64-linux", string(source))
			if !strings.Contains(asm, "    rep movsb\n") {
				t.Fatal("byte-copy kernel is absent")
			}
			if stackCopy.MatchString(asm) {
				t.Fatal("byte-copy operands use a stack exchange")
			}
		})
	}
}
