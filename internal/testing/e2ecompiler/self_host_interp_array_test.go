package e2ecompiler

import (
	"runtime"
	"testing"
)

var interpArrayRecvCases = []struct {
	name string
	src  string
}{
	{"generic-receiver", "function (xs: T[]) second(): T { return xs[1]; }\nfunction main(): i32 {\n  let xs: i32[] = [3, 7, 2];\n  if (xs.second() != 7) { return 1; }\n  let ws: string[] = [\"a\", \"b\"];\n  if (ws.second() != \"b\") { return 2; }\n  return 7;\n}\n"},
	{"receiver-with-closure-param", "function (xs: T[]) count_if(p: (T) => boolean): i32 {\n  let n: i32 = 0;\n  let i: i32 = 0;\n  while (i < xs.len()) {\n    if (p(xs[i])) { n = n + 1; }\n    i = i + 1;\n  }\n  return n;\n}\nfunction main(): i32 {\n  let xs: i32[] = [1, 5, 9];\n  return xs.count_if((v: i32): boolean => { return v > 2; }) + 5;\n}\n"},
	{"pinned-receiver", "pub function (arr: i32[]) second(): i32 { return arr[1]; }\nfunction main(): i32 {\n  let xs: i32[] = [3, 7, 2];\n  if (xs.second() != 7) { return 1; }\n  return 7;\n}\n"},

	{"builtins-alongside-method", "function (xs: T[]) head(): T { return xs[0]; }\nfunction main(): i32 {\n  let xs: i32[] = [1, 2, 3];\n  let ys: i32[] = xs.append(4).with(0, 5);\n  if (ys.len() != 4 || ys[0] != 5) { return 1; }\n  if (ys.head() != 5) { return 2; }\n  return 7;\n}\n"},
	{"struct-and-scalar-dispatch", "struct Box { v: i32 }\nfunction (b: Box) get(): i32 { return b.v; }\nfunction (s: string) twice(): string { return s + s; }\nfunction main(): i32 {\n  let b: Box = Box { v: 3 };\n  if (b.get() != 3) { return 1; }\n  if (\"ab\".twice() != \"abab\") { return 2; }\n  return 7;\n}\n"},
}

func TestSelfHostInterpArrayReceiver(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
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
