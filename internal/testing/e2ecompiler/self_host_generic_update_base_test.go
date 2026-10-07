package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostGenericUpdateBase(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, source string }{
		{"empty-array-assignment", `
struct Box[T] { items: T[], count: i32 }
function (b: Box[T]) clear(): Box[T] {
    b = Box { ...b, items: [] };
    return b;
}
function main(): i32 {
    let b: Box[string] = Box { items: ["retained"], count: 1 };
    let c: Box[string] = b.clear();
    if (c.items.len() != 0 || c.count != 1) { return 1; }
    if (b.items.len() != 1 || b.items[0] != "retained") { return 2; }
    return 0;
}`},
		{"nested-update-from-other-instantiation", `
struct Box[T] { item: T, count: i32 }
function (b: Box[T]) replace(other: Box[i64]): (Box[i64], Box[T]) {
    return (Box { ...other, count: 9 }, Box { ...b, count: 7 });
}
function main(): i32 {
    let a: Box[string] = Box { item: "keep", count: 1 };
    let b: Box[i64] = Box { item: 4294967301i64, count: 2 };
    let (c, d) = a.replace(b);
    if (c.item != 4294967301i64 || c.count != 9) { return 1; }
    if (d.item != "keep" || d.count != 7) { return 2; }
    if (a.count != 1 || b.count != 2) { return 3; }
    return 0;
}`},
		{"persistent-vector-empty-tail", `
import "std/pvec";
function main(): i32 {
    let old: pvec.PVec[string] = pvec.pvec_new[string]();
    let i: i32 = 0;
    while (i < 40) { old = old.append("keep"); i = i + 1; }
    let changed: pvec.PVec[string] = old.with(39, "tail").with(0, "root");
    if (old.get_or(0, "") != "keep" || old.get_or(39, "") != "keep") { return 1; }
    if (changed.get_or(0, "") != "root" || changed.get_or(39, "") != "tail") { return 2; }
    if (changed.get_or(12, "") != "keep") { return 3; }
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "update.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}
