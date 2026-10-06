package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const qualifiedConstructorShadowingSource = `
enum Box[T] { Packed(T) }
enum A { Value(i32) }
enum B { Value(string) }
enum State { Idle, Busy }
struct Factory {}
impl Factory { function Packed(self: Self, n: i32): i32 { return n + 3; } }
function Idle(): i32 { return 11; }
function Ok(n: i32): Result[u8, string] { assert(n == 300); return Result.Ok(1u8); }
function Packed(n: i32): i32 { return n + 1; }
function Value(n: i32): B { assert(n == 3); return B.Value("function"); }
function wrap[T](x: T): Box[T] { return Box.Packed(x); }
function local_closure(n: i32): i32 {
  let Packed = (x: i32): i32 => { return x + 2; };
  return Packed(n);
}
function local_type_head(n: i32): i32 {
  let Box = Factory {};
  return Box.Packed(n);
}
function main(): i32 {
  let argv: string[] = args();
  let count = argv.len();
  assert(count == 1);
  let idle = Idle;
  assert(idle() == 11);
  let state: State = State.Idle;
  match (state) { Idle => {}, Busy => { assert(false); } }
  match (Ok(299 + count)) { Ok(n) => { assert(n == 1u8); }, Err(_) => { assert(false); } }
  assert(Packed(count) == count + 1);
  assert(local_closure(count) == count + 2);
  assert(local_type_head(count) == count + 3);
  match (wrap(argv[0])) { Packed(s) => { assert(s == argv[0]); } }
  let b: B = Value(3);
  match (b) { Value(s) => { assert(s == "function"); } }
  let call = Value;
  match (call(3)) { Value(s) => { assert(s == "function"); } }
  let Packed = 7;
  let boxed: Box[string] = Box.Packed("local");
  match (boxed) { Packed(s) => { assert(s == "local"); } }
  assert(Packed == 7);
  return 0;
}
`

// Qualified calls select the enum, while bare calls and values retain their
// lexical bindings, including after generic instantiation rechecks the AST.
func TestSelfHostQualifiedConstructorShadowing(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(t.TempDir(), "qualified.fern")
	if err := os.WriteFile(path, []byte(qualifiedConstructorShadowingSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	if out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("primary interpreter: %v\n%s", err, out)
	}
	if out, err := exec.Command(buildLangBinForInterp(t), "-interp", path).CombinedOutput(); err != nil {
		t.Fatalf("reference interpreter: %v\n%s", err, out)
	}
}
