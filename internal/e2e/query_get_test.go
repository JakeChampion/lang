package e2e

import "testing"

// Differential coverage for the std/url single-key query accessors
// across backends: query_get (first value / None / bare-flag),
// query_has, query_get_all (ordered multi-values / empty), form-decoding
// of both key and value (%20 and +), the empty query, and skipped empty
// pairs. Returns 42 iff every check holds. Each leg skips itself when
// its toolchain is absent.
const queryGetProg = `
import "std/url" as url;
function opt(o: Option[string], fb: string): string {
    match (o) { Some(v) => { return v; }, None => { return fb; } }
}
function main(): i32 {
    if (opt(url.query_get("a=1&b=2", "a"), "X") != "1") { return 1; }
    if (opt(url.query_get("a=1&b=2", "b"), "X") != "2") { return 2; }
    if (opt(url.query_get("a=1&a=2", "a"), "X") != "1") { return 3; }
    if (opt(url.query_get("a=1", "z"), "MISS") != "MISS") { return 4; }
    if (opt(url.query_get("flag&a=1", "flag"), "X") != "") { return 5; }
    if (opt(url.query_get("a%20b=c+d", "a b"), "X") != "c d") { return 6; }
    if (!url.query_has("a=1&b=2", "b") || url.query_has("a=1", "z")) { return 7; }
    var all: string[] = url.query_get_all("a=1&b=2&a=3", "a");
    if (all.len() != 2 || all[0] != "1" || all[1] != "3") { return 8; }
    var none: string[] = url.query_get_all("a=1", "z");
    if (none.len() != 0) { return 9; }
    if (opt(url.query_get("", "a"), "MISS") != "MISS") { return 10; }
    if (opt(url.query_get("a=1&&b=2", "b"), "X") != "2") { return 11; }
    return 42;
}
`

func TestQueryGetInterp(t *testing.T) {
	if got := runInterpExit(t, queryGetProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestQueryGetX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, queryGetProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestQueryGetWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, queryGetProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestQueryGetArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, queryGetProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
