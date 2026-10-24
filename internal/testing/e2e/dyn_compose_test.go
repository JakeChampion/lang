package e2e

import (
	"testing"
)

const dynComposeDowncastSrc = `trait P { type Item; function get(self: Self): Self::Item; }
struct B { v: i32 }
impl P for B { type Item = i32; function get(self: Self): i32 { return self.v; } }
function main(): i32 {
    let d: dyn P[Item = i32] = B { v: 41 };
    match (d as? B) {
        Some(b) => { return b.v + 1; },
        None => { return 0; }
    }
}
`

const dynComposeMultiSrc = `trait Get[T] { function get(self: Self): T; }
trait P { type Item; function pi(self: Self): Self::Item; }
struct W { v: i32 }
impl Get[i32] for W { function get(self: Self): i32 { return self.v; } }
impl P for W { type Item = i32; function pi(self: Self): i32 { return 1; } }
function sum(d: dyn Get[i32] + P[Item = i32]): i32 { return d.get() + d.pi(); }
function main(): i32 { return sum(W { v: 41 }); }
`

func TestInterpDynComposeDowncast(t *testing.T) {
	if code := runInterpExit(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}
func TestX86_64DynComposeDowncast(t *testing.T) {
	if out, code := compileAndRunX86_64(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestArm64DynComposeDowncast(t *testing.T) {
	if out, code := compileAndRunArm64(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestWASMDynComposeDowncast(t *testing.T) {
	if code := runWasm(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}

func TestInterpDynComposeMulti(t *testing.T) {
	if code := runInterpExit(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}
func TestX86_64DynComposeMulti(t *testing.T) {
	if out, code := compileAndRunX86_64(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestArm64DynComposeMulti(t *testing.T) {
	if out, code := compileAndRunArm64(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestWASMDynComposeMulti(t *testing.T) {
	if code := runWasm(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
