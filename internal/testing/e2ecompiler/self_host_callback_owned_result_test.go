package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const callbackOwnedResultSource = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function keep(own h: Handle): i32 { return h.close(); }
function call[T](f: () => T): T { return f(); }
function advance[T](own state: T, step: (own T) => T): T { return step(state); }
function transition(own state: Handle, read: () => Result[i32,string]): Result[Handle,string] {
 return match (read()) { Ok(value) => Ok(state), Err(e) => Err(e) };
}
function tick(own state: Handle, other: Handle): Result[Handle,string] {
 return transition(state, (): Result[i32,string] => Ok(other.fd));
}
function fresh_from(h: Handle): Handle { return call((): Handle => Handle { fd: h.fd }); }
function copied(h: Handle): i32 { return keep(fresh_from(h)); }
function keep_data(own xs: i32[]): i32 { return xs[0]; }
function take_data(slot: Cell[i32[]]): i32[] {
 let xs = slot.get();
 slot.set([]);
 return xs;
}
function main(): i32 {
 let state = advance(Handle { fd: 7 }, (own s: Handle): Handle => s);
 if (keep(state) != 7) { return 1; }
 if (keep(call((): Handle => Handle { fd: 9 })) != 9) { return 2; }
 let other = Handle { fd: 11 };
 let final_state = match (tick(Handle { fd: 13 }, other)) { Ok(value) => value, Err(_) => { return 3; } };
 if (keep(final_state) != 13) { return 4; }
 if (copied(other) != 11 || other.close() != 11) { return 5; }
 let slot = cell_new([19]);
 if (keep_data(take_data(slot)) != 19 || slot.get().len() != 0) { return 7; }
 return 0;
}
`

const callbackNestedCaptureSource = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function keep(own h: Handle): i32 { return h.close(); }
function twice[T](f: () => (() => T)): T { return f()(); }
function bad(h: Handle): i32 { return keep(twice((): (() => Handle) => (() => h))); }
`

func TestSelfHostCallbackOwnedResultRuntime(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, callbackOwnedResultSource, []string{"FERN_SANITIZE=1"})
			var cmd *exec.Cmd
			if target == e2eharness.TargetWasm32Wasi {
				cmd = e2eharness.RunWasmCore(t, bin)
			} else {
				cmd = runX86_64Bin(x86_64Runner(t), bin)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("owned callback result runtime: %v\n%s", err, out)
			}
			if target == e2eharness.TargetX86_64Linux && !bytes.Contains(out, []byte("live_bytes=0")) {
				t.Fatalf("missing clean sanitizer census:\n%s", out)
			}
		})
	}
}

const callbackAggregateCaptureSource = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function keep(own h: Handle): i32 { return h.close(); }
function call[T](f: () => T): T { return f(); }
struct Box { h: Handle }
function take(own box: Box): i32 { return keep(box.h); }
function bad(h: Handle): i32 {
 let value = call((): Box => { let box = Box { h: h }; return box; });
 return take(value);
}
`

const callbackArrayCaptureSource = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function call[T](f: () => T): T { return f(); }
function take(own xs: Handle[]): i32 { return xs.len(); }
function bad(h: Handle): i32 { return take(call((): Handle[] => [h])); }
`

const callbackBuiltinShadowSource = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
function keep(own h: Handle): i32 { return h.close(); }
function bad(h: Handle): i32 {
 let cell_new = (): Handle => h;
 return keep(cell_new());
}
`

const callbackBuiltinScalarShadowSource = `
function fresh(): i32 {
 let cell_new = (): i32 => 7;
 return cell_new();
}
`
