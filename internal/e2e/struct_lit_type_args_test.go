package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// Construction-site type arguments on a generic struct literal —
// `Box[i32] { val: 42 }` — the `[ TypeArgs ]` of the spec grammar's
// `StructLit` production (#6812). `Stack[i32] { items: [] }` is the shape
// that motivates it: no field value can pin `T`, so before this the only
// spelling was an annotation on the binding.
const structLitTypeArgsProg = `struct Box[T] { val: T }
struct Stack[T] { items: T[] }
struct Pair[A, B] { a: A, b: B }

function mk(): Box[i64] { return Box[i64] { val: 9 }; }

function main(): i32 {
    var b = Box[i32] { val: 20 };
    var s = Stack[i32] { items: [] };
    var p = Pair[i32, string] { a: 3, b: "hi" };
    var upd = Box[i32] { ...b, val: 7 };
    var m = mk();
    return b.val + s.items.len() + p.a + p.b.len() + upd.val + (m.val as i32);
}
`

func TestStructLitTypeArgs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(structLitTypeArgsProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	const want = 20 + 0 + 3 + 2 + 7 + 9
	// One subtest per backend: a leg that skips for a missing qemu /
	// wasmtime must not take the reachable legs down with it.
	t.Run("interp", func(t *testing.T) {
		if _, code := runFixtureInterp(t, p, ""); code != want {
			t.Errorf("interp = %d, want %d", code, want)
		}
	})
	t.Run("x86-64", func(t *testing.T) {
		if _, code := runFixtureX86_64(t, p, ""); code != want {
			t.Errorf("x86-64 = %d, want %d", code, want)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := runFixtureArm64(t, p, ""); code != want {
			t.Errorf("arm64 = %d, want %d", code, want)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if code := runWasm(t, structLitTypeArgsProg); code != want {
			t.Errorf("wasm = %d, want %d", code, want)
		}
	})
}
