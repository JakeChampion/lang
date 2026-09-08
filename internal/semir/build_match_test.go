package semir

import (
	"strings"
	"testing"
)

var matchFlowCases = []struct{ name, source, want string }{
	{"match-negative", `function pilot(): string { return choose(-1i32); }
function choose(tag: i32): string { return match (tag) { -1i32 => "negative", _ => "wrong" }; }`, "negative\n"},
	{"match-minimum", `function pilot(): string { return choose(-2147483648i32); }
function choose(tag: i32): string { return match (-tag) { -2147483648i32 => "minimum", _ => "wrong" }; }`, "minimum\n"},
	{"match-pattern-failure-preserves-state", `function pilot(): string { return choose(0i32); }
function choose(tag: i32): string {
  var items = ["before"];
  return match (tag) { 1i32 when { items = ["guard"]; false } => "wrong", _ => items[0] };
}`, "before\n"},
	{"match-first-arm", `function pilot(): string { return choose(0i32); }
function choose(tag: i32): string { return match (tag) { 0i32 => "first", 1i32 => "one", _ => "other" }; }`, "first\n"},
	{"match-scrutinee-return", `function pilot(): string {
  return match ({ return "subject exit"; }) { _ => "wrong" };
}`, "subject exit\n"},
	{"match-arm-continue", `function pilot(): string {
  var i = 0i32; var items = ["before"];
  while (i < 2i32) {
    i = i + 1i32;
    var selected = match (i) { 1i32 => { items = ["continued"]; continue; }, _ => items };
    return selected[0];
  }
  return "wrong";
}`, "continued\n"},
	{"match-literal", `function pilot(): string { return choose(1i32); }
function choose(tag: i32): string { return match (tag) { 0i32 => "zero", 1i32 => "one", _ => "other" }; }`, "one\n"},
	{"match-fallback", `function pilot(): string { return choose(9i32); }
function choose(tag: i32): string { return match (tag) { 0i32 => "zero", 1i32 => "one", _ => "other" }; }`, "other\n"},
	{"match-boolean", `function pilot(): string { return match (false) { true => "wrong", _ => "false" }; }`, "false\n"},
	{"match-scrutinee-once", `function pilot(): string {
  var count = 0i32;
  var items = match ({ count = count + 1i32; count }) { 0i32 => ["zero"], 1i32 => ["once"], _ => ["wrong"] };
  if (count == 1i32) { return items[0]; } return "repeated";
}`, "once\n"},
	{"match-false-guard-effects", `function pilot(): string {
  var items = ["before"];
  return match (1i32) { 1i32 when { items = ["guard"]; false } => "wrong", 1i32 => items[0], _ => "wrong" };
}`, "guard\n"},
	{"match-unselected-guard", `function pilot(): string {
  return match (1i32) { 0i32 when fault() => "wrong", 1i32 => "skipped", _ => "wrong" };
}
function fault(): boolean { var empty: boolean[] = []; return empty[0]; }`, "skipped\n"},
	{"match-at-binding", `function pilot(): string {
  return match (7i32) { n @ 7i32 when n == 7i32 => "bound", _ => "wrong" };
}`, "bound\n"},
	{"match-arm-shadow", `function pilot(): string {
  var n = 9i32;
  return match (7i32) { n @ 7i32 when false => "wrong", _ => if (n == 9i32) { "outer" } else { "leaked" } };
}`, "outer\n"},
	{"match-borrowed-join", `function pilot(): string { return choose(1i32); }
function choose(tag: i32): string {
  var child = match (tag) { 1i32 => { var parent = [["held"]]; parent[0] }, _ => ["other"] };
  var i = 0i32; while (i < 64i32) { var churn = [["churn"]]; i = i + 1i32; }
  return child[0];
}`, "held\n"},
	{"match-body-return", `function pilot(): string { return choose(1i32); }
function choose(tag: i32): string {
  var items = match (tag) { 1i32 => { return "body exit"; }, _ => ["live"] }; return items[0];
}`, "body exit\n"},
	{"match-body-live", `function pilot(): string { return choose(0i32); }
function choose(tag: i32): string {
  var items = match (tag) { 1i32 => { return "body exit"; }, _ => ["live"] }; return items[0];
}`, "live\n"},
	{"match-guard-return", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  return match (1i32) { 1i32 when { if (flag) { return "guard exit"; } false } => "wrong", _ => "other" };
}`, "guard exit\n"},
	{"match-all-return", `function pilot(): string {
  return match (1i32) { 1i32 => { return "all exit"; }, _ => { return "other exit"; } };
}`, "all exit\n"},
	{"match-condition-break", `function pilot(): string {
  loop {
    while (match (1i32) { 1i32 => { break; }, _ => false }) {}
    return "wrong loop";
  }
  return "outer";
}`, "outer\n"},
}

func TestBuildMatchFlow(t *testing.T) {
	for _, tc := range append(matchFlowCases, tupleMatchCases...) {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planProgramUnits(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBuildMatchRejectsUnsupportedPatterns(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"string", `function pilot(): string { return match ("x") { "x" => "yes", _ => "no" }; }`, "match scrutinee requires"},
		{"wide", `function pilot(): string { return match (1i64) { 1i64 => "yes", _ => "no" }; }`, "match scrutinee requires"},
		{"tuple-string-literal", `function pilot(): string { return match ((1i32, "x")) { (_, "x") => "yes", _ => "no" }; }`, "tuple literal requires"},
		{"range", `function pilot(): string { return match (1i32) { 0i32..=2i32 => "yes", _ => "no" }; }`, "unsupported semantic match pattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			_, err := BuildProgram(prog, info)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want explicit %q", err, tc.want)
			}
		})
	}
}
