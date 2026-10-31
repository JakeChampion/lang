package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostThreeReferenceReturns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, src string }{{"lifetime", threeReferenceLifetime}, {"zero-tuple-allocations", threeReferenceAlloc}} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

func TestSelfHostThreeReferenceMetadata(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "three_ref_metadata.fern"), []byte(threeReferenceMetadata), 0600); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "three_ref_metadata.fern", "three-ref-metadata")
	for _, tc := range []struct{ name, want string }{
		{"triple-metadata", "findings=0 unproved=0 skipped="},
		{"call-balanced", "findings=0 unproved=0 skipped="},
		{"call-leak-primary", "leaked at return of v0"},
		{"call-leak-secondary", "leaked at return of v1"},
		{"call-leak-third", "leaked at return of v2"},
		{"call-return-primary", "findings=0 unproved=0 skipped="},
		{"call-return-secondary", "findings=0 unproved=0 skipped="},
		{"call-return-third", "findings=0 unproved=0 skipped="},
		{"callee-balanced", "findings=0 unproved=0 skipped="},
		{"callee-leak-secondary", "leaked at return of v1"},
		{"callee-leak-third", "leaked at return of v2"},
		{"callee-scalar-cannot-own-third", "leaked at return of v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runX86_64Bin(runner, driver, tc.name).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			t.Log(string(out))
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("want %q in %s", tc.want, out)
			}
			summary := "findings=0 unproved=0 skipped="
			if !strings.HasPrefix(tc.want, "findings=0") {
				summary = "findings=1 unproved=0 skipped="
			}
			if !strings.HasSuffix(strings.TrimSpace(string(out)), summary) {
				t.Errorf("want final summary %q in %s", summary, out)
			}
		})
	}
}

const threeReferenceAlloc = `@noinline function triple(a: i64[], b: i64[], c: i64[]): (i64[], i64[], i64[]) { return (a, b, c); }
function main(): i32 {
 let a=__alloc_i64(args().len()+1);a=a.with(0,17i64);
 let b=__alloc_i64(args().len()+1);b=b.with(0,23i64);
 let c=__alloc_i64(args().len()+1);c=c.with(0,29i64);
 let keep_a=a;let keep_b=b;let keep_c=c;let before=__heap_alloc_count();
 for i in 0..1000 { let (x,y,z)=triple(a,b,c);if (x[0]!=17i64 || y[0]!=23i64 || z[0]!=29i64) {return 91;} }
 if (__heap_alloc_count()-before!=0i64 || keep_a[0]!=17i64 || keep_b[0]!=23i64 || keep_c[0]!=29i64) {return 92;}
 return 0;
}`

const threeReferenceLifetime = `import "core/mem";
struct ThreeRefDropCell { p: usize }
impl mem.Drop for ThreeRefDropCell { function drop(self: Self): void { unsafe { __store_i64(self.p,__load_i64(self.p)+1i64); } } }
struct A { xs: string[], cell: ThreeRefDropCell }
struct B { label: string, cell: ThreeRefDropCell }
struct C { xs: string[], cell: ThreeRefDropCell }
@noinline function triple(a: A,b: B,c: C): (A,B,C) {return (a,b,c);}
@noinline function texts(a: string,b: string,c: string): (string,string,string) {return (a,b,c);}
@noinline function aliases(a: string[]): (string[],string[],string[]) {return (a,a,a);}
function main(): i32 { unsafe {
 let counter: i64[]=__alloc_i64(1);let ptr=(counter as usize)+8;
 for i in 0..100 {
  let s="a dynamic string with heap storage "+i.to_string();
  let a=A {xs:[s],cell:ThreeRefDropCell {p:ptr}};let keep_a=a;
  let b=B {label:s,cell:ThreeRefDropCell {p:ptr}};let keep_b=b;
  let c=C {xs:[s],cell:ThreeRefDropCell {p:ptr}};let keep_c=c;
  let (x,y,z)=triple(a,b,c);
  if (x.xs[0]!=s || y.label!=s || z.xs[0]!=s || keep_a.xs[0]!=s || keep_b.label!=s || keep_c.xs[0]!=s) {return 91;}
  let (p,q,r)=texts(s,"",if (i%2==0) {"tiny"} else {s});
  if (p!=s || q!="" || r!=if (i%2==0) {"tiny"} else {s}) {return 92;}
  let (u,v,w)=aliases(x.xs);let grown=w.append("tail");
  if (u.len()!=1 || v.len()!=1 || grown.len()!=2 || z.xs[0]!=s) {return 93;}
 }
 if (counter[0]!=300i64) {return 94;}
 return 0;
} }
`

const threeReferenceMetadata = `import "./physicalbody"; import "./ssasem"; import "./semrecords"; import "./typeinfo";
import "./rccertify"; import "./rcsigs"; import "./ssa"; import "./ir"; import "./util";
function inst(kind: i32,result: i32,args: i32[],imm: i32,str: string): ssa.SInst {return ssa.SInst {kind_tag:kind,result:result,args:args,imm:imm,str:str};}
function call(result: i32,name: string,args: i32[]): ssa.SInst {return inst(11,result,args,0,name);}
function ret(v: i32): ssa.STerm {return ssa.STerm {kind_tag:1,cond:0,target:0,t:0,f:0,value:v};}
function array_type(): typeinfo.Type {return typeinfo.TypeArray {elem:typeinfo.TypeString {tag:0},view:false};}
function triple_type(): typeinfo.Type {return typeinfo.TypeTuple {elements:[array_type(),array_type(),array_type()]};}
function main(): i32 {
 let name=args()[1];let in_callee=util.str_has_prefix(name,"callee-");
 let params: typeinfo.Type[]=[];let modes: i32[]=[];let result: typeinfo.Type=typeinfo.TypeI32 {width:32,unsigned:false,is_char:false,polymorphic:false};
 if (in_callee) {params=[array_type(),array_type(),array_type()];modes=[ssasem.counted_mode(),ssasem.counted_mode(),ssasem.counted_mode()];result=triple_type();}
 let returned: i32= -1;
 if (name=="call-return-primary") {returned=0;}
 if (name=="call-return-secondary") {returned=1;}
 if (name=="call-return-third") {returned=2;}
 if (returned>=0) {result=array_type();}
 let m=physicalbody.Metadata {contracts:[ssasem.Contract {name:"triple",params:[],modes:[],result:triple_type()},ssasem.Contract {name:"case",params:params,modes:modes,result:result}],records:semrecords.no_records(),enums:[],finalizers:[]};
 let abis=physicalbody.abis(physicalbody.Collection {producers:[m],rows:[]});let abi=abis.lookup("triple");
 if (!abi.known || !abi.pair_first || abi.pair_second!= -1 || abi.split_second!= -1 || abi.split_third!= -1 || !abi.result_unit) {print("invalid triple metadata");return 91;}
 if (name=="triple-metadata") {print("findings=0 unproved=0 skipped=");return 0;}
 let ops: ir.Op[]=[ir.op_call_pair_word(0),ir.op_call_pair_word(1),ir.op_ret_pair_word(0),ir.op_ret_pair_word(1),ir.op_ret_scalar_word(1)];
 let ins: ssa.SInst[]=[];let term=ret(8);let np: i32=0;
 if (in_callee) {
  np=3;ins=[inst(6,0,[],0,""),inst(6,1,[],1,""),inst(6,2,[],2,"")];
  if (name!="callee-leak-secondary") {ins=ins.append(inst(59,3,[1],2,"ret_word"));}
  if (name!="callee-leak-third") {ins=ins.append(inst(59,4,[2],if (name=="callee-scalar-cannot-own-third") {4} else {3},"ret_word"));}
  term=ret(0);
 } else {
  ins=[call(0,"triple",[]),inst(59,1,[],0,"call_word"),inst(59,2,[],1,"call_word")];
  if (name!="call-leak-primary" && returned!=0) {ins=ins.append(call(3,"__fern_rc_dec",[0]));}
  if (name!="call-leak-secondary" && returned!=1) {ins=ins.append(call(4,"__fern_rc_dec",[1]));}
  if (name!="call-leak-third" && returned!=2) {ins=ins.append(call(5,"__fern_rc_dec",[2]));}
  ins=ins.append(inst(1,8,[],0,""));if (returned>=0) {term=ret(returned);}
 }
 let f=ssa.SFunc {name:"case",nparams:np,nvals:9,blocks:[ssa.SBlock {id:0,insts:ins,term:term,preds:[]}],entry:0,takes_env:false};
 let r=rccertify.certify(f,ops,abis.lookup("case"),abis,rcsigs.table(),rcsigs.op_table());
 for x in r.findings {print(rccertify.render("case",x));}
 print("findings="+util.i32_to_string(r.findings.len())+" unproved="+util.i32_to_string(r.unproved)+" skipped="+r.skipped);
 return 0;
}`
