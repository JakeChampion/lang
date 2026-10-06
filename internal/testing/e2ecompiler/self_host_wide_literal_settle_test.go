package e2ecompiler

import (
	"strings"
	"testing"
)

// An unsuffixed integer literal reaching a 64-bit destination through a
// value-position if / match or a builtin variant constructor is read at that
// destination, as a bare literal is (#10343). Each case matches native
// `fern -check`.
var wideSettleAccepts = []struct{ name, src string }{
	{"if-expr-i64", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	{"match-expr-u64", `function main(): i32 { let n = 1; let x: u64 = match (n) { 1 => 5, _ => 9 }; return x as i32; }`},
	{"some-literal-option-i64", `function main(): i32 { let o: Option[i64] = Some(40); match (o) { Some(v) => { return v as i32; }, None => { return 0; } } }`},
	{"err-literal-result-i64", `function g(): Result[i32, i64] { return Err(40); } function main(): i32 { match (g()) { Ok(v) => { return v; }, Err(e) => { return e as i32; } } }`},
	{"ok-i32-into-result-i64", `function g(n: i32): Result[i64, i32] { return Ok(n); } function main(): i32 { match (g(3)) { Ok(v) => { return v as i32; }, Err(e) => { return e; } } }`},
}

// The settle is literal-shaped: an i32 VALUE still does not become an i64 by
// passing through an if-expression or `Some`.
var wideSettleRejects = []struct{ name, src string }{
	{"some-i32-var-into-option-i64", `function main(): i32 { let n: i32 = 5; let o: Option[i64] = Some(n); return 0; }`},
	{"if-expr-i32-var-into-i64", `function main(): i32 { let n: i32 = 5; let c = 1; let x: i64 = if (c > 0) { n } else { 9 }; return x as i32; }`},
}

func TestSelfHostWideLiteralSettle(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)
	for _, tc := range wideSettleAccepts {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			if code, stderr := runSelfHostChecker(t, checkerBin, runner, tc.src); code != 0 || strings.TrimSpace(stderr) != "" {
				t.Errorf("self-host checker exited %d, want 0 and no diagnostic\n%s", code, stderr)
			}
		})
	}
	for _, tc := range wideSettleRejects {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			code, stderr := runSelfHostChecker(t, checkerBin, runner, tc.src)
			if code == 0 || !strings.Contains(stderr, "E003") {
				t.Errorf("self-host checker exited %d, want an E003 rejection\n%s", code, stderr)
			}
		})
	}
}
