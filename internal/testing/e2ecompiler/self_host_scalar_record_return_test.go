package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// The caller only projects each result. All four scalar fields must survive
// the return at full width, including NaN payloads, negative zero and u32's
// high bit. A reference field, five fields or an escaping use stays boxed.
func TestSelfHostScalarRecordReturns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range scalarReturnCases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(c.source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					var cmd *exec.Cmd
					switch target {
					case "x86-64-linux":
						cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
					case "arm64-linux":
						_, qemu := arm64Tooling(t)
						cmd = runArm64Bin(qemu, cli.arm64Binary(t, src))
					default:
						cmd = exec.Command("wasmtime", "run", cli.emit(t, src, target))
					}
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != c.want {
						t.Errorf("%s: %v: got %q, want %q", target, err, out, c.want)
					}
				})
			}
		})
	}
}

const scalarRecordWide = `import "std/i64"; import "std/u64";
struct Quad { a: u64, b: u64, c: u64, d: u64 }
@noinline function quad(x: u64): Quad { return Quad {a:x,b:x+1u64,c:x+2u64,d:x+3u64}; }
function main(): i32 {
 let before=__heap_alloc_count(); let acc=0u64; let i=0;
 while(i<1000) {let q=quad((i+1) as u64);acc=acc+q.a+q.b+q.c+q.d;i=i+1;}
 let count=__heap_alloc_count()-before;
 print(count.to_string());print(acc.to_string());return 0;
}
`
const scalarRecordMixed = `import "std/i64";
struct Mixed { a: f64, b: f64, n: i32, flag: boolean }
@noinline function mixed(i: i32): Mixed {return Mixed {a:f64_from_bits(0x7ff8000000000042i64+(i as i64)),b:f64_from_bits(0x8000000000000000u64 as i64),n:0-i,flag:i%2==0};}
function main(): i32 {
 let before=__heap_alloc_count();let i=0;
 while(i<1000) {let q=mixed(i);if(f64_bits(q.a)!=(0x7ff8000000000042i64+(i as i64))) {return 11;}if(f64_bits(q.b)!=(0x8000000000000000u64 as i64)) {return 12;}if(q.n!=0-i || q.flag!=(i%2==0)) {return 13;}i=i+1;}
 let count=__heap_alloc_count()-before;print(count.to_string());return 0;
}
`
const scalarRecordNarrow = `import "std/i64";
struct Narrow { a: f32, b: u32, c: i64, flag: boolean }
@noinline function narrow(i: i32): Narrow {return Narrow {a:1.25 as f32,b:0xffff0000u32+(i as u32),c:0x123456789abcdef0i64+(i as i64),flag:i%2==0};}
function main(): i32 {
 let before=__heap_alloc_count();let i=0;
 while(i<1000) {let q=narrow(i);if((q.a as f64)!=1.25 || q.b!=0xffff0000u32+(i as u32) || q.c!=0x123456789abcdef0i64+(i as i64) || q.flag!=(i%2==0)) {return 14;}i=i+1;}
 let count=__heap_alloc_count()-before;print(count.to_string());return 0;
}
`
const scalarRecordReference = `import "std/i64";
struct Referenced { a: i64, b: i64, c: i64, xs: i64[] }
@noinline function record(i:i32,xs:i64[]):Referenced {return Referenced {a:i as i64,b:2i64,c:3i64,xs:xs};}
function main():i32 {let xs:i64[]=[17i64];let before=__heap_alloc_count();let i=0;while(i<1000){let q=record(i,xs);if(q.a!=(i as i64) || q.xs[0]!=17i64){return 15;}i=i+1;}let count=__heap_alloc_count()-before;print(count.to_string());return 0;}
`
const scalarRecordFive = `import "std/i64";
struct Five { a:i64,b:i64,c:i64,d:i64,e:i64 }
@noinline function record(i:i32):Five {return Five {a:i as i64,b:2i64,c:3i64,d:4i64,e:5i64};}
function main():i32 {let before=__heap_alloc_count();let i=0;while(i<1000){let q=record(i);if(q.a!=(i as i64)||q.e!=5i64){return 16;}i=i+1;}let count=__heap_alloc_count()-before;print(count.to_string());return 0;}
`
const scalarRecordEscapes = `import "std/i64";
struct Quad { a:i64,b:i64,c:i64,d:i64 }
@noinline function record(i:i32):Quad {return Quad {a:i as i64,b:2i64,c:3i64,d:4i64};}
@noinline function consume(q:Quad):i64 {return q.a+q.b+q.c+q.d;}
function main():i32 {let before=__heap_alloc_count();let i=0;while(i<1000){if(consume(record(i))!=(i as i64)+9i64){return 17;}i=i+1;}let count=__heap_alloc_count()-before;print(count.to_string());return 0;}
`

const scalarRecordSmallPhis = `import "std/i64";
struct One {a:usize}
struct Two {a:i32,b:f64}
struct Three {a:u8,b:i64,c:boolean}
@noinline function one(x:usize):One{return One {a:x};}
@noinline function two(i:i32):Two {let q=Two {a:i,b:3.5};if(i%2==0){q=Two {a:0-i,b:0.0-2.5};}return q;}
@noinline function three(i:i32):Three {return Three {a:(i%251) as u8,b:0x123456789abcdef0i64+(i as i64),c:i%2==0};}
function main():i32 {let before=__heap_alloc_count();let i=0;while(i<1000){let x=one(0xfedcba9876543210u64 as usize);let y=two(i);let z=three(i);if(x.a!=(0xfedcba9876543210u64 as usize)){return 21;}if(i%2==0){if(y.a!=0-i || y.b!=0.0-2.5){return 22;}}else{if(y.a!=i||y.b!=3.5){return 23;}}if(z.a!=(i%251) as u8||z.b!=0x123456789abcdef0i64+(i as i64)||z.c!=(i%2==0)){return 24;}i=i+1;}let count=__heap_alloc_count()-before;print(count.to_string());return 0;}
`
const scalarRecordPressure = `import "std/i64";
struct Quad {a:i64,b:i64,c:i64,d:i64}
@noinline function lanes(own xs:i64[],a:i64,b:i64,c:i64,d:i64,e:i64,f:i64,g:i64,h:i64,i:i64,j:i64,k:i64,l:i64,m:i64):Quad {return Quad {a:xs[0]+a+b+c+d,b:e+f+g,c:h+i+j,d:k+l+m};}
@noinline function run(a:i64,b:i64,c:i64,d:i64,e:i64,f:i64,g:i64,h:i64,i:i64,j:i64,k:i64,l:i64,m:i64):i32 {
 let q=lanes([31i64],a,b,c,d,e,f,g,h,i,j,k,l,m);
 let r=lanes([53i64],m,l,k,j,i,h,g,f,e,d,c,b,a);
 if(q.a!=41i64||q.b!=18i64||q.c!=27i64||q.d!=36i64){return 25;}
 if(r.a!=99i64||r.b!=24i64||r.c!=15i64||r.d!=6i64){return 26;}
 if(a+b+c+d+e+f+g+h+i+j+k+l+m!=91i64){return 27;}return 0;
}
function main():i32 {let code=run(1i64,2i64,3i64,4i64,5i64,6i64,7i64,8i64,9i64,10i64,11i64,12i64,13i64);if(code!=0){return code;}print("0");return 0;}
`

var scalarReturnCases = []struct{ name, source, want string }{
	{"four-wide", scalarRecordWide, "0\n2008000\n"},
	{"mixed-float-bits", scalarRecordMixed, "0\n"},
	{"narrow-float", scalarRecordNarrow, "0\n"},
	{"one-two-three-phis", scalarRecordSmallPhis, "0\n"},
	{"cleanup-pressure", scalarRecordPressure, "0\n"},
	{"padding-tail-arguments", scalarPaddingTail, "0\n"},
	{"reference-field", scalarRecordReference, "1000\n"},
	{"five-fields", scalarRecordFive, "1000\n"},
	{"escaping-result", scalarRecordEscapes, "1000\n"},
	{"function-value", scalarRecordFunctionValue, "1000\n"},
	{"finalizer", scalarRecordFinalizer, "1000\n"},
}

func TestSelfHostScalarRecordReturnsInstrumented(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, c := range scalarReturnCases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					bin := compileCPUProfile(t, cli, c.source, target, "", "-sanitize")
					var cmd *exec.Cmd
					switch target {
					case "x86-64-linux":
						cmd = runX86_64Bin(cli.runner, bin)
					case "arm64-linux":
						_, qemu := arm64Tooling(t)
						cmd = runArm64Bin(qemu, bin)
					default:
						cmd = exec.Command("wasmtime", "run", bin)
					}
					var stdout, stderr bytes.Buffer
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
					if err := cmd.Run(); err != nil || stdout.String() != c.want {
						t.Fatalf("%v: stdout %q, want %q; stderr %s", err, &stdout, c.want, &stderr)
					}
					m := leak.FindStringSubmatch(stderr.String())
					if len(m) != 3 || m[1] != m[2] {
						t.Fatalf("unbalanced sanitized run: %s", &stderr)
					}
				})
			}
		})
	}
	t.Setenv("QEMU_CPU", "max")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, mode := range []string{"cpu", "alloc"} {
			harden := []bool{false}
			if target == "arm64-linux" {
				harden = append(harden, true)
			}
			for _, h := range harden {
				label := target + "/" + mode
				if h {
					label += "/harden"
				}
				t.Run(label, func(t *testing.T) {
					flags := []string{"-sanitize"}
					if h {
						flags = append(flags, "-harden")
					}
					bin := compileCPUProfile(t, cli, scalarRecordPressure, target, mode, flags...)
					var cmd *exec.Cmd
					if target == "arm64-linux" {
						_, qemu := arm64Tooling(t)
						cmd = runArm64Bin(qemu, bin)
					} else {
						cmd = runX86_64Bin(cli.runner, bin)
					}
					var stdout, stderr bytes.Buffer
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
					if err := cmd.Run(); err != nil || stdout.String() != "0\n" {
						t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
					}
					m := leak.FindStringSubmatch(stderr.String())
					if len(m) != 3 || m[1] != m[2] {
						t.Fatalf("unbalanced instrumented run: %s", &stderr)
					}
				})
			}
		}
	}
}

const scalarRecordFunctionValue = `import "std/i64";
struct Quad {a:i64,b:i64,c:i64,d:i64}
@noinline function record(i:i32):Quad {return Quad {a:i as i64,b:2i64,c:3i64,d:4i64};}
@noinline function via(f:(i32)=>Quad,i:i32):Quad{return f(i);}
function main():i32 {let f:(i32)=>Quad=record;let before=__heap_alloc_count();let i=0;while(i<1000){let q=via(f,i);if(q.a!=(i as i64)||q.b+q.c+q.d!=9i64){return 28;}i=i+1;}let count=__heap_alloc_count()-before;print(count.to_string());return 0;}
`
const scalarRecordFinalizer = `import "core/mem";import "std/i64";
struct Watched {a:i64,p:usize}
impl mem.Drop for Watched {function drop(self:Self):void {__store_i64(self.p,__load_i64(self.p)+1i64);}}
@noinline function record(i:i32,p:usize):Watched {return Watched {a:i as i64,p:p};}
function main():i32 {let counter:i64[]=__alloc_i64(1);let p=(counter as usize)+8;__store_i64(p,0i64);let before=__heap_alloc_count();let i=0;while(i<1000){let q=record(i,p);if(q.a!=(i as i64)){return 29;}i=i+1;}let count=__heap_alloc_count()-before;if(__load_i64(p)!=1000i64||counter.len()!=1){return 30;}print(count.to_string());return 0;}
`

// Two arguments stay live across increment, producing two saved registers and
// an alignment word. Removing that word must preserve sum's prepared arguments.
const scalarPaddingTail = `@noinline function increment(c:i64):i64{return c+1i64;}
@noinline function sum(a:i64,b:i64,c:i64):i64{return a+b+c;}
@noinline function tail(a:i64,b:i64,c:i64):i64{let d=increment(c);return sum(a,b,d);}
function main():i32{if(tail(1i64,2i64,3i64)!=7i64){return 31;}print("0");return 0;}
`
