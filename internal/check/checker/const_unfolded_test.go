package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestConstReferencesCheckBeforeFolding(t *testing.T) {
	prog, _, err := modload.LoadSource(`const N: i32 = 7;
const TBL: u64[] = [0xfffffffffffffffeu64, 2u64];
const M = N + 1;
const WORD = "fern";
function main(): i32 {
    let w: string = WORD;
    return N + M + (TBL[1] as i32) + w.len() + LATE;
}
const LATE: i32 = 3;
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestStdlibConstsCheckBeforeFolding(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "std/string";
import "std/f64";
function main(): i32 {
    match (f64.parse_f64("1.5")) {
        Ok(v) => { return v.to_string().len(); },
        Err(_) => { return 0; }
    }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
}
