package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const interpScopeContractHook = `pub function contract_scope_frames(): i32 {
  let root = new_env().bind_decl("outer", v_int(7), "i32").bind_decl("wide", v_wide(9000000000i64, false), "i64");
  let first = scope_env(root, true).bind_decl("local", v_int(2), "i32");
  let second = scope_env(first, true).bind_decl("outer", VString { v: "shadow" }, "string");
  if (second.decl_type("outer") != "string" || second.decl_type("wide") != "i64" || !second.binds_name("local")) { return 1; }
  let changed = second.update("wide", v_wide(11i64, false)).update("local", v_int(9));
  let one = trim_env(changed, first.depth);
  let all = trim_env(changed, root.depth);
  if (value_text(one.lookup("outer")) != "7" || value_text(one.lookup("local")) != "9" || value_text(all.lookup("wide")) != "11") { return 2; }
  if (all.binds_name("local") || all.decl_type("local") != "" || value_text(root.lookup("wide")) != "9000000000" || value_text(first.lookup("local")) != "2") { return 3; }
  let boxed = cellify_env(root, ["outer"]);
  let nested = scope_env(boxed, true).bind("inner", v_int(3));
  let raw = match (nested.raw_value("outer")) { Some(v) => v, None => { exit(4); } };
  if (!is_cell(raw) || !cell_slot_set(raw, v_int(13))) { return 5; }
  if (value_text(boxed.lookup("outer")) != "13" || value_text(trim_env(nested, boxed.depth).lookup("outer")) != "13" || value_text(root.lookup("outer")) != "7") { return 6; }
  let witness_before = __heap_alloc_count();
  let witness = new_env().bind("witness", v_int(17));
  if (value_text(witness.lookup("witness")) != "17" || __heap_alloc_count() <= witness_before) { return 7; }
  let large = new_env();
  for i in 0..2000 { large = large.bind("slot_" + util.i32_to_string(i), v_int(i)); }
  let child = scope_env(scope_env(large, true).bind("a", v_int(3)), true).bind("b", v_int(5));
  let before = __heap_alloc_count();
  let popped = trim_env(child, large.depth);
  let after = __heap_alloc_count();
  if (after != before) { eprint(after - before); return 8; }
  if (popped.names.len() != 2000 || popped.binds_name("a") || popped.binds_name("b") || value_text(popped.lookup("slot_1999")) != "1999") { return 9; }
  let flat = flattened_env(changed);
  if (flat.depth != 0 || value_text(flat.lookup("outer")) != "shadow" || value_text(flat.lookup("local")) != "9" || value_text(flat.lookup("wide")) != "11") { return 10; }
  return 0;
}

function contract_session_line(s: Session, source: string): (Session, boolean, Value) {
  let mod = parser.parse_module(lexer.tokenize("function __repl(): void { " + source + " }"));
  return s.run(mod, "__repl");
}

pub function contract_frame_sessions(): i32 {
  let s = new_session();
  let (a, shows_a, va) = contract_session_line(s, "let x = 3;");
  if (shows_a || is_err(va)) { return 11; }
  let (b, shows_b, vb) = contract_session_line(a, "if (true) { let local = 4; x = x + local; }");
  if (shows_b || is_err(vb)) { return 12; }
  let (c, shows_c, vc) = contract_session_line(b, "x;");
  if (!shows_c || c.show(vc) != "7") { return 13; }
  let (d, shows_d, vd) = contract_session_line(c, "local;");
  if (!shows_d || !is_err(vd)) { return 14; }
  let (e, shows_e, ve) = contract_session_line(d, "let get = (): i32 => x;");
  if (shows_e || is_err(ve)) { return 15; }
  let (f, shows_f, vf) = contract_session_line(e, "x = 9;");
  if (shows_f || is_err(vf)) { return 16; }
  let (g, shows_g, vg) = contract_session_line(f, "get();");
  if (!shows_g || g.show(vg) != "7") { return 17; }
  let (h, shows_h, vh) = contract_session_line(g, "let saved: () => i32 = (): i32 => 0; if (true) { let local = 5; saved = (): i32 => local + x; }");
  if (shows_h || is_err(vh)) { return 18; }
  let (j, shows_j, vj) = contract_session_line(h, "saved();");
  if (!shows_j || j.show(vj) != "14") { return 19; }
  let (k, shows_k, vk) = contract_session_line(j, "if (true) { x = x + 1; let hidden = 5; missing(); }");
  if (!shows_k || !is_err(vk)) { return 20; }
  let (l, shows_l, vl) = contract_session_line(k, "x;");
  if (!shows_l || l.show(vl) != "10") { return 21; }
  let (m, shows_m, vm) = contract_session_line(l, "hidden;");
  if (!shows_m || !is_err(vm)) { return 22; }
  return 0;
}
`

var interpScopePrograms = []struct{ name, src string }{
	{"typed-shadow", `function main():i32 { let outer:i64=7; let total=0; if(true){let outer:string="abc";total=outer.len();} if(outer!=7i64 || total!=3){return 1;} return 0; }`},
	{"labeled-unwind", `function main():i32 { let count=0; outer: while(true){let i=0;while(i<10){count=count+1;if(count>=11){break outer;}i=i+1;}} if(count!=11){return 1;}return 0;}`},
	{"iteration-captures", `function main():i32 {let fs:(()=>i32)[]=[];let k=0;while(k<3){let n=k;fs=fs.append(()=>n);let other=():i32=>{let n=99;n=n+1;return n;};other();k=k+1;}let total=0;for f in fs {total=total+f();}if(total!=3){return 1;}return 0;}`},
	{"outer-array-alias", `function main():i32 {let xs=[1,2];let keep=xs;for i in 0..4 {let local=i;xs=xs.with(0,xs[0]+local).append(local);}if(keep[0]!=1 || keep.len()!=2 || xs[0]!=7 || xs.len()!=6){return 1;}return 0;}`},
	{"match-shadow", `enum E { A(i32), B } function main():i32 {let n=7;let total=0;match(A(4)){A(n)=>{let local=n+1;total=local;},B=>{total=2;}}if(n!=7 || total!=5){return 1;}return 0;}`},
	{"escaped-block-capture", `function make():()=>i32 {let outer=1;if(true){let inner=2;let get=():i32=>outer+inner;outer=4;inner=3;return get;}return ():i32=>0;}function main():i32 {let f=make();if(f()!=7){return 1;}return 0;}`},
}

func TestSelfHostInterpScopeFrames(t *testing.T) {
	oracle := buildLangBinForInterp(t)
	for _, tc := range interpScopePrograms {
		t.Run("oracle/"+tc.name, func(t *testing.T) {
			if code := interpExit(t, oracle, tc.src); code != 0 {
				t.Fatalf("Go interpreter: exit %d, want 0", code)
			}
		})
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp.fern", "drivers/rundriver.fern")
	f, err := os.OpenFile(filepath.Join(dir, "interp.fern"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + interpScopeContractHook); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeResolveProject(t, dir, map[string]string{"interp_scope.fern": `import "./interp";
import "./drivers/rundriver";
function main():i32 {
 let av=args();
 if(av.len()>1 && av[1]=="eval") {
  let value=interp.eval_module(rundriver.parse_stdin("scope_input"));
  let (ok,status)=interp.exit_status(value);
  if(ok){return status;}
  eprint(interp.new_session().show(value));
  return 254;
 }
 let code=interp.contract_scope_frames();
 if(code!=0){return code;}
 return interp.contract_frame_sessions();
}
`})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "interp_scope.fern", "interp_scope", target)
			t.Run("contracts", func(t *testing.T) {
				out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
				if err != nil || len(out) != 0 {
					t.Fatalf("frame contract: %v\n%s", err, out)
				}
			})
			for _, tc := range interpScopePrograms {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, "eval")
					cmd.Stdin = strings.NewReader(tc.src)
					out, err := cmd.CombinedOutput()
					if err != nil || len(out) != 0 {
						t.Fatalf("interpreted scope: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
