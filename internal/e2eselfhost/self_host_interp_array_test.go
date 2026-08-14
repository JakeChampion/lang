package e2eselfhost

import (
	"runtime"
	"testing"
)

// interpArrayRecvCases are array-receiver programs run through the self-host
// interpreter's stdin driver. The interpreter derives a receiver type name
// from the runtime value variant, and an array had none, so before #6809 every
// `xs.<method>(…)` that was not one of the three zero-import builtins (`len`
// / `append` / `with`) stopped at "receiver has no type name to dispatch on" —
// which is the whole of std/array. Those cases need the module loader and live
// in TestSelfHostInterpStdlibModload; the ones here are self-contained.
//
// Each case is oracle-checked against the native interpreter rather than
// against a hardcoded exit code, so what they pin is the language's behaviour.
var interpArrayRecvCases = []struct {
	name string
	src  string
}{
	// The element-polymorphic receiver spelling, which is the only array
	// receiver the language allows (a concrete `i32[]` receiver is E021). One
	// declaration serves every element type, exactly as the untyped value
	// model needs.
	{"generic-receiver", "function (xs: T[]) second(): T { return xs[1]; }\nfunction main(): i32 {\n  var xs: i32[] = [3, 7, 2];\n  if (xs.second() != 7) { return 1; }\n  var ws: string[] = [\"a\", \"b\"];\n  if (ws.second() != \"b\") { return 2; }\n  return 7;\n}\n"},
	// A method taking a closure, dispatched on an array: resolving the
	// receiver is only half of it, the body has to run too.
	{"receiver-with-closure-param", "function (xs: T[]) count_if(p: (T) => boolean): i32 {\n  var n: i32 = 0;\n  var i: i32 = 0;\n  while (i < xs.len()) {\n    if (p(xs[i])) { n = n + 1; }\n    i = i + 1;\n  }\n  return n;\n}\nfunction main(): i32 {\n  var xs: i32[] = [1, 5, 9];\n  return xs.count_if(function (v: i32): boolean { return v > 2; }) + 5;\n}\n"},
	// The `__method_Array_<name>` convention — the free-function spelling the
	// front end dispatches `xs.<name>(…)` to for helpers whose element type is
	// concrete, and which std/array's sorted_asc / join / sum are written as.
	{"method-array-convention", "pub function __method_Array_second(arr: i32[]): i32 { return arr[1]; }\nfunction main(): i32 {\n  var xs: i32[] = [3, 7, 2];\n  if (xs.second() != 7) { return 1; }\n  return 7;\n}\n"},

	// CONTROLS. The fix gives arrays a receiver type name for the first time,
	// so what can break is dispatch on everything else.
	{"builtins-alongside-method", "function (xs: T[]) head(): T { return xs[0]; }\nfunction main(): i32 {\n  var xs: i32[] = [1, 2, 3];\n  var ys: i32[] = xs.append(4).with(0, 5);\n  if (ys.len() != 4 || ys[0] != 5) { return 1; }\n  if (ys.head() != 5) { return 2; }\n  return 7;\n}\n"},
	{"struct-and-scalar-dispatch", "struct Box { v: i32 }\nfunction (b: Box) get(): i32 { return b.v; }\nfunction (s: string) twice(): string { return s + s; }\nfunction main(): i32 {\n  var b: Box = Box { v: 3 };\n  if (b.get() != 3) { return 1; }\n  if (\"ab\".twice() != \"abab\") { return 2; }\n  return 7;\n}\n"},
}

// TestSelfHostInterpArrayReceiver drives each case through a compiled
// `interp_run.fern` — the stdin driver, which resolves no imports, so every
// case here is self-contained.
//
// Host modes mirror TestSelfHostInterpEnumConstructors: on Apple Silicon the
// driver is built for arm64-darwin through the in-process Mach-O path, off it
// with the Go x86-64 backend.
func TestSelfHostInterpArrayReceiver(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")
	}
	interpBin := buildLangBinForInterp(t)

	for _, tc := range interpArrayRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 7 {
				t.Fatalf("native interp oracle exited %d, want 7 — the case itself is wrong, not the self-host engine", want)
			}
			if got := runDriverExit(t, runner, driverBin, []byte(tc.src)); got != want {
				t.Errorf("self-host interp exited %d, want %d (native interp oracle)", got, want)
			}
		})
	}
}
