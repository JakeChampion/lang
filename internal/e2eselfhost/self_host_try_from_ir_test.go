package e2eselfhost

import "testing"

// tryFromIRCases pin the FROM-CONVERTING `?` (#2697). When a
// `Result[_, E1]` is propagated with `?` through a function returning
// `Result[_, E2]` and E1 != E2, the failure path cannot forward the source
// `Err(E1)` unchanged: it returns `Err(E2.from(e))`, the native checker's
// tryConvertErrViaFrom desugar. Nothing in the source names `from`, so the
// tree-shake has to keep it for the `?`.
//
// The `err-converts*` cases are the essential ones: `read(0)` yields
// `Err(IoErr{42})`; the convert wraps it to `AppErr{42+50}` = 92, so the
// handler reads e.code == 92. Forwarding the box unconverted would surface
// e.code == 42, so 92-vs-42 is a direct witness that the conversion fired.
// Oracles stay <= 120 (the wasm exit-code clamp, #2908).
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
	// Ok path with DIFFERING error types: read(1)? unwraps Ok(8) (no
	// conversion on the success path), +1 = 9.
	{"ok-prop", `function run(ok: i32): Result[i32, AppErr] { var v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	// Error path — THE conversion witness: read(0)? propagates Err(IoErr{42}),
	// converted to Err(AppErr{92}); the handler reads e.code == 92 (NOT 42).
	{"err-converts", `function run(ok: i32): Result[i32, AppErr] { var v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 92},
	// Convert + arithmetic on the converted error field: 92 + 3 = 95 (pins that
	// the bound `e` is the converted AppErr, not the source IoErr).
	{"err-converts-add", `function run(ok: i32): Result[i32, AppErr] { var v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(e) => { return e.code + 3; } } }`, 95},
	// Two `?` in a row, both Ok across the error-type boundary: 8 + 8 = 16.
	{"two-ok", `function run(ok: i32): Result[i32, AppErr] { var a = read(ok)?; var b = read(ok)?; return Ok(a + b); } function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 16},
	// The SECOND `?` short-circuits and converts: read(1)?=8 then read(0)?
	// converts Err(IoErr{42}) -> Err(AppErr{92}); handler reads e.code == 92.
	{"second-errs", `function run(): Result[i32, AppErr] { var a = read(1)?; var b = read(0)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 92},
}

func tryFromIRSrc(mainBody string) string {
	return tryFromIRPrelude + "\n" + mainBody + "\n"
}

// TestSelfHostTryFromIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code and the leak census.
func TestSelfHostTryFromIR(t *testing.T) {
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
