package e2ecompiler

import "testing"

const tryFromIRPrelude = `trait FromIo { function from(e: IoErr): Self; }
struct IoErr { code: i32 }
struct AppErr { code: i32 }
impl FromIo for AppErr { function from(e: IoErr): AppErr { return AppErr { code: e.code + 50 }; } }
function read(ok: i32): Result[i32, IoErr] {
    if (ok == 0) { return Err(IoErr { code: 42 }); }
    return Ok(8);
}
`

var tryFromIRCases = []struct {
	name string
	main string
	want int
}{
	{"ok-prop", `function run(ok: i32): Result[i32, AppErr] { let v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	{"err-converts", `function run(ok: i32): Result[i32, AppErr] { let v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 92},
	{"err-converts-add", `function run(ok: i32): Result[i32, AppErr] { let v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(e) => { return e.code + 3; } } }`, 95},
	{"two-ok", `function run(ok: i32): Result[i32, AppErr] { let a = read(ok)?; let b = read(ok)?; return Ok(a + b); } function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 16},
	{"second-errs", `function run(): Result[i32, AppErr] { let a = read(1)?; let b = read(0)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 92},
}

func tryFromIRSrc(mainBody string) string {
	return tryFromIRPrelude + "\n" + mainBody + "\n"
}

func TestSelfHostTryFromIR(t *testing.T) {
	boxedProbes(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryFromIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tryFromIRSrc(tc.main), target, "FERN_LEAKCHECK=1")
				if code != tc.want {
					t.Fatalf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
