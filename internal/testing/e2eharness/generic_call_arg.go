package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

const GenericCallArgProgram = `import "std/option";
import "std/result";
struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box[T] { xs: [] }; }
function size(b: Box[i32]): i32 { return b.xs.len(); }
function size_s(b: Box[string]): i32 { return b.xs.len() + 1; }
struct Keep[T] { b: Box[T] }
function (k: Keep[T]) swap[T](b: Box[T]): i32 { return k.b.xs.len() + b.xs.len() + 2; }

function main(): i32 {
    let r: Result[Box[i32], string] = Err("x");
    let o: Option[Box[string]] = None;
    let k: Keep[i32] = Keep[i32] { b: mk() };
    // 0 + 1 + 0 + 1 + 2
    let n: i32 = size(mk()) + size_s(mk()) + r.unwrap_or(mk()).xs.len() + size_s(o.unwrap_or(mk())) + k.swap(mk());
    if (n == 4) {
        return 0;
    }
    return n + 10;
}
`

func WriteGenericCallArgProgram(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(GenericCallArgProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
