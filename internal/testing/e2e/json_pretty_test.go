package e2e

import "testing"

const jsonPrettyProg = `
import "std/json" as json;
import "std/string";
function main(): i32 {
    match (json.parse("{\"name\":\"fern\",\"nums\":[1,2,3],\"nested\":{\"ok\":true},\"e\":{},\"a\":[]}")) {
        Ok(v) => {
            let p: string = json.json_encode_pretty(v, 2);
            if (!p.contains("\n") || !p.contains("  \"name\": \"fern\"")) { return 1; }
            if (!p.contains("    1")) { return 2; }          // nested-array element indent
            if (!p.contains("{}") || !p.contains("[]")) { return 3; }  // empties compact
            match (json.parse(p)) {
                Ok(v2) => { if (json.json_encode(v2) != json.json_encode(v)) { return 4; } },
                Err(_) => { return 5; }
            }
            if (json.json_encode_pretty(JNumber("7"), 4) != "7") { return 6; }
            if (json.json_encode_pretty(JString("x"), 4) != "\"x\"") { return 7; }
            return 42;
        },
        Err(_) => { return 99; }
    }
}
`

func TestJsonPrettyInterp(t *testing.T) {
	if got := runInterpExit(t, jsonPrettyProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestJsonPrettyX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, jsonPrettyProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestJsonPrettyWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, jsonPrettyProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestJsonPrettyArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, jsonPrettyProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
