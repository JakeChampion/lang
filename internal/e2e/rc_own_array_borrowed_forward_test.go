package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A borrowed array parameter owns no reference until its first replacement.
// Forwarding it to an own callee must buy that first reference and transfer
// subsequent replacements. Check values, reclamation and underflows separately.
const ownArrayBorrowedForwardSrc = `@noinline
function update(own xs: i32[], n: i32): i32[] { xs = xs.with(0, n); return xs; }
@noinline
function forward(xs: i32[], rounds: i32): i32[] {
    var i: i32 = 0;
    while (i < rounds) { xs = update(xs, i); i = i + 1; }
    return xs;
}
function churn(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var xs: i32[] = [1, 2, 3];
        var next: i32[] = forward(xs, 32);
        if (xs[0] != 1 || xs[1] != 2 || xs[2] != 3) { return 1; }
        if (next[0] != 31 || next[1] != 2 || next[2] != 3) { return 2; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    if (churn() != 0) { return 1; }
    var before: i64 = __heap_bump_bytes();
    if (churn() != 0) { return 2; }
    if (__rc_underflow_count() != 0) { return 3; }
    if (__heap_bump_bytes() != before) { return 4; }
    return 0;
}`

func ownArrayPointerForwardSource(ty, initial, replacement, checks string) string {
	return `@noinline
function update(own xs: ` + ty + `, n: i32): ` + ty + ` { xs = xs.with(0, ` + replacement + `); return xs; }
@noinline
function forward(xs: ` + ty + `): ` + ty + ` {
    var i: i32 = 0;
    while (i < 32) { xs = update(xs, i); i = i + 1; }
    return xs;
}
function churn(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var xs: ` + ty + ` = ` + initial + `;
        var next: ` + ty + ` = forward(xs);
        if (` + checks + `) { return 1; }
        i = i + 1;
    }
    return 0;
}
` + ownArrayBorrowedForwardSrc[strings.Index(ownArrayBorrowedForwardSrc, "function main()"):]
}

func TestOwnArrayBorrowedForwardLifetime(t *testing.T) {
	previous := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = previous }()

	cases := []struct {
		name string
		src  string
	}{
		{"repeated-update", ownArrayBorrowedForwardSrc},
		{"identity-result", strings.ReplaceAll(strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"xs = xs.with(0, n); return xs;", "return xs;"), "next[0] != 31", "next[0] != 1")},
		{"no-transfer-path", strings.ReplaceAll(strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"forward(xs, 32)", "forward(xs, 0)"), "next[0] != 31", "next[0] != 1")},
		{"fresh-replacement", strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"xs = xs.with(0, n); return xs;", "return [n, xs[1], xs[2]];")},
		{"i64-elements", strings.ReplaceAll(strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"i32[]", "i64[]"), "xs.with(0, n)", "xs.with(0, n as i64)")},
		{"f64-elements", strings.ReplaceAll(strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"i32[]", "f64[]"), "xs.with(0, n)", "xs.with(0, n as f64)")},
		{"early-return-owned", strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"xs = update(xs, i); i = i + 1;", "xs = update(xs, i); if (i == 31) { return xs; } i = i + 1;")},
		{"scalar-return-releases-owned", strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"function forward(xs: i32[], rounds: i32): i32[]", "function forward(xs: i32[], rounds: i32): i32"),
			"    return xs;", "    return xs[0];"),
			"var next: i32[] = forward(xs, 32);", "var value: i32 = forward(xs, 32); var next: i32[] = [value, 2, 3];")},
		{"string-elements", ownArrayPointerForwardSource("string[]", `["old" + "!", "keep" + "!"]`, `"new" + "!"`,
			`xs[0] != "old!" || xs[1] != "keep!" || next[0] != "new!" || next[1] != "keep!"`)},
		{"nested-elements", ownArrayPointerForwardSource("i32[][]", `[[1, 2], [3, 4]]`, `[n, n + 1]`,
			`xs[0][0] != 1 || xs[0][1] != 2 || xs[1][0] != 3 || xs[1][1] != 4 || next[0][0] != 31 || next[0][1] != 32 || next[1][0] != 3 || next[1][1] != 4`)},
		{"owned-replacement-reuses", strings.ReplaceAll(ownArrayBorrowedForwardSrc,
			"var i: i32 = 0;\n    while (i < rounds) { xs = update(xs, i); i = i + 1; }",
			"xs = update(xs, 0); var mark: i64 = __heap_bump_bytes(); var i: i32 = 0;\n"+
				"    while (i < rounds) { xs = update(xs, i); assert(__heap_bump_bytes() == mark); i = i + 1; }")},
	}
	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) int
	}{
		{"x86-64", func(t *testing.T, src string) int { _, code := compileAndRunX86_64FreeOn(t, src); return code }},
		{"arm64", func(t *testing.T, src string) int { _, code := compileAndRunArm64FreeOn(t, src); return code }},
		{"wasm", runWasm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fixture := range cases {
				t.Run(fixture.name, func(t *testing.T) {
					if got := tc.run(t, fixture.src); got != 0 {
						t.Fatalf("exit %d, want 0: values, zero underflows and flat warmed-up heap", got)
					}
				})
			}
		})
	}
}
