package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A type parameter written as the bare variable (`a: C`) and pinned only by a
// generic enum's variant construction binds to the whole enum instantiation;
// it used to bind nothing, leaving the call to the uninstantiated template,
// which the self-host then reported as undefined (#10209).
const variantArgInferSrc = `enum Bag[T] { Items(T[]), Empty }
function size[C](a: C, b: C): i32 {
    var n: i32 = 0;
    match (a) { Items(xs) => { n = n + xs.len(); }, Empty => {} }
    match (b) { Items(xs) => { n = n + xs.len() * 10; }, Empty => {} }
    return n;
}
function main(): i32 { return size(Items([1, 2, 3]), Empty) + size(Empty, Items(["a", "b"])); }
`

func TestSelfHostVariantArgBindsBareTypeVarX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "variant_arg_infer.fern")
	if err := os.WriteFile(src, []byte(variantArgInferSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src)
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 23 {
		t.Fatalf("exit=%d, want 23 (stderr %q)", exit, stderr)
	}
}
