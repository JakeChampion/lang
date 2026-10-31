package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostThreeReferenceForwarding(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(threeReferenceForwarding), 0600); err != nil {
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
}

const threeReferenceForwarding = `import "core/mem";
struct ForwardDropCell { p: usize }
impl mem.Drop for ForwardDropCell {function drop(self: Self): void {unsafe {__store_i64(self.p,__load_i64(self.p)+1i64);}}}
struct ForwardBox { xs: string[], cell: ForwardDropCell }
@noinline function forward(t: (ForwardBox,ForwardBox,ForwardBox),same: boolean): (ForwardBox,ForwardBox,ForwardBox) {
 if (same) {return t;}
 return (t.2,t.0,t.1);
}
@noinline function arrays(t: (i64[],i64[],i64[]),same: boolean): (i64[],i64[],i64[]) {
 if (same) {return t;}
 return (t.2,t.0,t.1);
}
function main(): i32 {unsafe {
 let counter: i64[]=__alloc_i64(1);let ptr=(counter as usize)+8;
 for i in 0..100 {
  let text="a heap-backed forwarded tuple string "+i.to_string();
  let a=ForwardBox {xs:[text],cell:ForwardDropCell {p:ptr}};
  let b=ForwardBox {xs:[text+"b"],cell:ForwardDropCell {p:ptr}};
  let c=ForwardBox {xs:[text+"c"],cell:ForwardDropCell {p:ptr}};
  let tuple=(a,b,c);let keep=tuple;
  let (x,y,z)=forward(tuple,i%2==0);
  if (x.xs[0]!=if (i%2==0) {text} else {text+"c"}) {return 91;}
  if (y.xs[0]!=if (i%2==0) {text+"b"} else {text}) {return 92;}
  if (z.xs[0]!=if (i%2==0) {text+"c"} else {text+"b"}) {return 93;}
  if (keep.0.xs[0]!=text || keep.1.xs[0]!=text+"b" || keep.2.xs[0]!=text+"c") {return 94;}
 }
 if (counter[0]!=300i64) {return 95;}
 let a=__alloc_i64(args().len()+1);a=a.with(0,17i64);
 let b=__alloc_i64(args().len()+1);b=b.with(0,23i64);
 let c=__alloc_i64(args().len()+1);c=c.with(0,29i64);
 let tuple=(a,b,c);let keep=tuple;let before=__heap_alloc_count();
 for i in 0..1000 {
  let (x,y,z)=arrays(tuple,i%2==0);
  if (x[0]!=if (i%2==0) {17i64} else {29i64}) {return 96;}
  if (y[0]!=if (i%2==0) {23i64} else {17i64}) {return 97;}
  if (z[0]!=if (i%2==0) {29i64} else {23i64}) {return 98;}
 }
 if (__heap_alloc_count()-before!=0i64 || keep.0[0]!=17i64 || keep.1[0]!=23i64 || keep.2[0]!=29i64) {return 99;}
 return 0;
}}
`
