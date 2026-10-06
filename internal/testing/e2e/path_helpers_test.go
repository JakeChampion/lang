package e2e

import "testing"

// Differential coverage for the std/path helpers added alongside the
// original join/parent/file_name/extension/clean set:
// path_is_absolute, path_stem, and path_with_extension — including the
// multi-extension case ("archive.tar.gz"), hidden-file dots
// (".bashrc"), the no-extension and empty-extension paths, and
// directory preservation. Returns 42 iff every exact check holds. Each
// leg skips itself when its toolchain is absent.
const pathHelpersProg = `
import "std/path" as path;
function main(): i32 {
    if (!path.path_is_absolute("/etc/hosts")) { return 1; }
    if (path.path_is_absolute("etc/hosts")) { return 2; }
    if (path.path_is_absolute("")) { return 3; }
    if (path.path_is_absolute("./x")) { return 4; }
    if (path.path_stem("a/b/foo.txt") != "foo") { return 5; }
    if (path.path_stem("archive.tar.gz") != "archive.tar") { return 6; }
    if (path.path_stem(".bashrc") != ".bashrc") { return 7; }
    if (path.path_stem("foo") != "foo") { return 8; }
    if (path.path_stem("/a/b/") != "b") { return 9; }
    if (path.path_stem("") != "") { return 10; }
    if (path.path_with_extension("a/b/foo.txt", "md") != "a/b/foo.md") { return 11; }
    if (path.path_with_extension("foo", "md") != "foo.md") { return 12; }
    if (path.path_with_extension("archive.tar.gz", "") != "archive.tar") { return 13; }
    if (path.path_with_extension(".bashrc", "txt") != ".bashrc.txt") { return 14; }
    if (path.path_with_extension("/a/b/foo.txt", "json") != "/a/b/foo.json") { return 15; }
    if (path.path_with_extension("foo.txt", "") != "foo") { return 16; }
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
