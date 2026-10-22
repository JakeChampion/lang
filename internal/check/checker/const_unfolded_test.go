package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

// A program is checked before constfold substitutes its consts wherever the
// pipeline stops at checking (the LSP, the oracle's tests), so a const has to
// resolve to its type there.
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

// The stdlib's own private consts resolve too: std/string's Eisel-Lemire
// table and std/f64's Dragonbox tables are module-private u64 arrays.
func TestStdlibConstsCheckBeforeFolding(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "std/string";
import "std/f64";
function main(): i32 {
    match ("1.5".parse_float()) {
        Some(v) => { return v.to_string().len(); },
        None => { return 0; }
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
