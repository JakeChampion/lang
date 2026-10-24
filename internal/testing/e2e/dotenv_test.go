package e2e

import "testing"

const dotenvProg = `
import "std/dotenv" as dotenv;
import "core/map";
function main(): i32 {
    let m: Map[string, string] = dotenv.parse("# c\nHOST=localhost\nPORT = 8080\nexport TOKEN=abc\nMSG=\"a\\nb\"\nRAW='x\\ny'\nEMPTY=\nno_eq\n=nokey\nHOST=override\r\n");
    if (m.get_or("HOST", "?") != "override") { return 1; }
    if (m.get_or("PORT", "?") != "8080") { return 2; }
    if (m.get_or("TOKEN", "?") != "abc") { return 3; }
    if (m.get_or("MSG", "?") != "a\nb") { return 4; }
    if (m.get_or("RAW", "?") != "x\\ny") { return 5; }
    if (!m.contains("EMPTY") || m.get_or("EMPTY", "?") != "") { return 6; }
    if (m.contains("no_eq")) { return 7; }
    if (m.len() != 6) { return 8; }   // HOST, PORT, TOKEN, MSG, RAW, EMPTY
    return 42;
}
`

func TestDotenvInterp(t *testing.T) {
	if got := runInterpExit(t, dotenvProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestDotenvX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, dotenvProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestDotenvWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, dotenvProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestDotenvArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, dotenvProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
