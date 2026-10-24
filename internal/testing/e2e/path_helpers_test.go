package e2e

import "testing"

const pathHelpersProg = `
import "std/path" as path;
function main(): i32 {
    if (!path.is_absolute("/etc/hosts")) { return 1; }
    if (path.is_absolute("etc/hosts")) { return 2; }
    if (path.is_absolute("")) { return 3; }
    if (path.is_absolute("./x")) { return 4; }
    if (path.stem("a/b/foo.txt") != "foo") { return 5; }
    if (path.stem("archive.tar.gz") != "archive.tar") { return 6; }
    if (path.stem(".bashrc") != ".bashrc") { return 7; }
    if (path.stem("foo") != "foo") { return 8; }
    if (path.stem("/a/b/") != "b") { return 9; }
    if (path.stem("") != "") { return 10; }
    if (path.with_extension("a/b/foo.txt", "md") != "a/b/foo.md") { return 11; }
    if (path.with_extension("foo", "md") != "foo.md") { return 12; }
    if (path.with_extension("archive.tar.gz", "") != "archive.tar") { return 13; }
    if (path.with_extension(".bashrc", "txt") != ".bashrc.txt") { return 14; }
    if (path.with_extension("/a/b/foo.txt", "json") != "/a/b/foo.json") { return 15; }
    if (path.with_extension("foo.txt", "") != "foo") { return 16; }
    return 42;
}
`

func TestPathHelpersInterp(t *testing.T) {
	if got := runInterpExit(t, pathHelpersProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestPathHelpersX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, pathHelpersProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestPathHelpersWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, pathHelpersProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestPathHelpersArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, pathHelpersProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
