package e2eselfhost

import (
	"strings"
	"testing"
)

// FERN_APPEND_REPORT: one line per `a = a.append(v)` self-reassign naming the
// arr_push-vs-arr_push_owned verdict and the rule that decided.
//
// The verdict is the OWNERSHIP axis, not native `-append-report`'s
// copy-vs-in-place one: a plain __fern_arr_push leaves the superseded buffer to
// its holders on a grow, and the store after it is what gives this frame's
// reference back (an aliased local's cow-guarded release, a parameter's
// ownership flag), which is where the self-built compiler's retention used to
// come from (#7954). An rctrace line attributes such a block to an address
// inside __fern_arr_push, so every append in the program aggregates to one site
// and only the lowering can say which source site chose which push.
//
// Both verdicts are asserted, because the report's whole value is telling them
// apart: a version that printed one string for every site would pass a
// smoke test and answer nothing.
const appendReportSrc = `function build(n: i32): i32[] {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}
function onparam(ys: i32[]): i32[] {
    ys = ys.append(9);
    return ys;
}
function main(): i32 { var a: i32[] = build(4); var b: i32[] = onparam(a); return b.len(); }
`

// appendReportStderr compiles src through the CLI on the AST lowering, which is
// the only lowering that writes the report, and returns the compiler's stderr.
func appendReportStderr(t *testing.T, cli *strictCLI, src string, env ...string) string {
	t.Helper()
	_, diags, err := cli.tryEmit(t, "x86-64-linux", src, append([]string{"FERN_SEM_IR="}, env...)...)
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, diags)
	}
	return diags
}

func TestSelfHostAppendReport(t *testing.T) {
	cli := newStrictCLI(t)

	// Off by default: a run without the variable prints nothing, the same
	// contract util.arr_push_cliff_report keeps.
	if quiet := appendReportStderr(t, cli, appendReportSrc); strings.Contains(quiet, "append-report:") {
		t.Fatalf("report printed without FERN_APPEND_REPORT set:\n%s", quiet)
	}

	got := appendReportStderr(t, cli, appendReportSrc, "FERN_APPEND_REPORT=1")

	var lines []string
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "append-report:") {
			lines = append(lines, ln)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 report lines, got %d:\n%s", len(lines), got)
	}

	for _, tc := range []struct {
		fn, verdict, recv, reason string
	}{
		// A fresh local nothing else references: the grow reclaims.
		{"build", "owned", "xs", "sole owner"},
		// A parameter: the buffer is the caller's, so the grow may not
		// free it; the store's ownership flag releases the superseded
		// generation this frame owns (#3457).
		{"onparam", "shared", "ys", "parameter target"},
	} {
		var line string
		for _, ln := range lines {
			if strings.Contains(ln, tc.fn+":") {
				line = ln
			}
		}
		if line == "" {
			t.Fatalf("no report line for %s:\n%s", tc.fn, got)
		}
		for _, want := range []string{tc.verdict, tc.recv, tc.reason} {
			if !strings.Contains(line, want) {
				t.Errorf("%s: line %q missing %q", tc.fn, line, want)
			}
		}
	}
}

// FERN_APPEND_REPORT's EXPRESSION-position arm: `xs.append(v)` that is not a
// self-reassign — a call argument, a var-init, a return.
//
// The axis differs from the self-reassign arm's and that is deliberate. There
// the question is whether the frame owns the buffer it grows; here it is
// whether the push COPIES, because a bracketed receiver cannot take the grow helper's
// in-place path (append_copy_recv_slot) and so always reallocates. Both print
// under one prefix so a single grep sees every append a program makes.
const exprAppendSrc = `function sink(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var t: i32 = sink(a.append(20));
    var b: i32[] = a.append(30);
    exit((t + b.len()) % 7);
    return 0;
}
`

func TestSelfHostAppendReportExprPosition(t *testing.T) {
	cli := newStrictCLI(t)

	if quiet := appendReportStderr(t, cli, exprAppendSrc); strings.Contains(quiet, "append-report:") {
		t.Fatalf("report printed without FERN_APPEND_REPORT set:\n%s", quiet)
	}

	got := appendReportStderr(t, cli, exprAppendSrc, "FERN_APPEND_REPORT=1")

	var lines []string
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "append-report:") {
			lines = append(lines, ln)
		}
	}
	// Both appends are expression-position; neither is a self-reassign, so the
	// leak arm contributes nothing and these two are the whole report.
	if len(lines) != 2 {
		t.Fatalf("want 2 report lines, got %d:\n%s", len(lines), got)
	}
	for _, ln := range lines {
		for _, want := range []string{"main:", "COPIES", "  a  ", "bracketed receiver"} {
			if !strings.Contains(ln, want) {
				t.Errorf("line %q missing %q", ln, want)
			}
		}
	}
	// The two sites are told apart by position — a report that collapsed them
	// would pass every assertion above and locate nothing.
	if lines[0] == lines[1] {
		t.Errorf("both appends reported identically (%q); the line:col must separate them", lines[0])
	}
}
