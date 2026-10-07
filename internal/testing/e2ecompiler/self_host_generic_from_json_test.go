package e2ecompiler

import (
	"path/filepath"
	"testing"
)

func TestSelfHostGenericFromJson(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			t.Run("associated-result-self", func(t *testing.T) {
				path, err := filepath.Abs("../../../conformance/cases/generic_assoc_result_self/main.fern")
				if err != nil {
					t.Fatal(err)
				}
				stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
			t.Run("generic-fields-and-errors", func(t *testing.T) {
				stderr, code := cli.exitOf(t, `
import "std/json";
@derive(json.FromJson)
struct Pair[A, B] { first: A, rest: B[], maybe: Option[B] }
function number(): Result[Pair[i64, string], string] {
    return Pair.from_json("{\"first\":4294967301,\"rest\":[],\"maybe\":null}");
}
function main(): i32 {
    match (number()) {
        Ok(p) => {
            if (p.first != 4294967301i64 || p.rest.len() != 0) { return 1; }
            match (p.maybe) { Some(_) => { return 2; }, None => {} }
        },
        Err(_) => { return 3; }
    }
    let other: Result[Pair[string, i32], string] = Pair.from_json("{\"first\":\"kept\",\"rest\":[3,7],\"maybe\":9}");
    match (other) {
        Ok(p) => {
            if (p.first != "kept" || p.rest.len() != 2 || p.rest[0] != 3 || p.rest[1] != 7) { return 4; }
            match (p.maybe) { Some(v) => { if (v != 9) { return 5; } }, None => { return 6; } }
        },
        Err(_) => { return 7; }
    }
    let bad: Result[Pair[string, i32], string] = Pair.from_json("{\"first\":\"kept\",\"rest\":[\"wrong\"]}");
    match (bad) { Ok(_) => { return 8; }, Err(e) => { if (e.len() == 0) { return 9; } } }
    return 0;
}`, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		})
	}
}
