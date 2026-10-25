package modload_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestPubPackageFollowsTheNearestManifest(t *testing.T) {
	const owner = "[package]\nname = \"app\"\n"
	cases := []struct {
		name    string
		files   map[string]string
		refused bool
	}{
		{"subdirectory", map[string]string{
			"fern.toml":  owner,
			"sub/x.fern": `pub(package) function helper(): i32 { return 42; }`,
			"main.fern":  `import "./sub/x"; function main(): i32 { return x.helper(); }`,
		}, false},
		{"normalized-path", map[string]string{
			"fern.toml":  owner,
			"sub/x.fern": `pub(package) function helper(): i32 { return 42; }`,
			"main.fern":  `import "./sub/../sub/x"; function main(): i32 { return x.helper(); }`,
		}, false},
		{"nested-manifest-with-the-same-name", map[string]string{
			"fern.toml":     owner,
			"sub/fern.toml": owner,
			"sub/x.fern":    `pub(package) function helper(): i32 { return 42; }`,
			"main.fern":     `import "./sub/x"; function main(): i32 { return x.helper(); }`,
		}, true},
		{"outside-the-manifest-tree", map[string]string{
			"app/fern.toml":  owner,
			"app/sub/x.fern": `pub(package) function helper(): i32 { return 42; }`,
			"main.fern":      `import "./app/sub/x"; function main(): i32 { return x.helper(); }`,
		}, true},
		{"fallback-stays-a-directory", map[string]string{
			"sub/x.fern": `pub(package) function helper(): i32 { return 42; }`,
			"main.fern":  `import "./sub/x"; function main(): i32 { return x.helper(); }`,
		}, true},
		{"constant-and-type", map[string]string{
			"fern.toml":  owner,
			"sub/x.fern": `pub(package) struct Item { value: i32 } pub(package) const VALUE: i32 = 42;`,
			"main.fern":  `import "./sub/x"; function main(): i32 { let item: x.Item = x.Item { value: x.VALUE }; return item.value; }`,
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, tc.files)
			prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "pub(package)") {
					t.Fatalf("got %v, want a package visibility refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPubPackageEnumVariantsFollowOwner(t *testing.T) {
	for index, source := range []string{
		`function main(): i32 { let node = nodes.Leaf(42); return 42; }`,
		`function main(): i32 { let node = nodes.Empty; return 42; }`,
		`function main(): i32 { let node = nodes.Node.Leaf(42); return 42; }`,
		`function main(): i32 { let node: nodes.Node = nodes.Leaf(42); match (node) { nodes.Leaf(n) => { return n; }, nodes.Empty => { return 0; } } return 0; }`,
		`function main(): i32 { let node = Leaf(42); return 42; }`,
		`function main(): i32 { let node = Empty; return 42; }`,
		`function main(): i32 { let node = nodes.make(); match (node) { Leaf(n) => { return n; }, Empty => { return 0; } } return 0; }`,
	} {
		for _, scope := range []string{"pub", "pub(package)", ""} {
			for _, nested := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/form=%d/nested=%t", scope, index, nested), func(t *testing.T) {
					files := map[string]string{
						"fern.toml":      "[package]\nname = \"app\"\n",
						"main.fern":      `import "./sub/nodes";` + source,
						"sub/nodes.fern": scope + ` enum Node { Empty, Leaf(i32) } pub function make(): Node { return Leaf(42); }`,
					}
					if nested {
						files["sub/fern.toml"] = "[package]\nname = \"other\"\n"
					}
					dir := writeFiles(t, files)
					prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
					if err == nil {
						_, err = checker.Check(prog)
					}
					refused := scope == "" || scope == "pub(package)" && nested
					if refused {
						if err == nil || !(strings.Contains(err.Error(), "pub(package)") || strings.Contains(err.Error(), "not exported") || strings.Contains(err.Error(), "cannot find") || strings.Contains(err.Error(), "not visible")) {
							t.Fatalf("got %v, want a visibility refusal", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := checker.Check(prog); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestStdlibPackageHelpersArePrivate(t *testing.T) {
	for _, source := range []string{
		`import "std/f64"; function main(): i32 { let n = f64.pow10_hi(0); return 0; }`,
		`import "std/f64"; function main(): i32 { let n = f64.pow10_lo(0); return 0; }`,
		`import "std/f64"; function main(): i32 { let n = f64.pow10_log2(0); return 0; }`,
		`import "std/io_buffered"; function main(): i32 { io_buffered.buf_push_u64(0 as usize, 1u64); return 0; }`,
		`import "std/io_buffered"; function main(): i32 { io_buffered.buf_push_i64(0 as usize, 1i64); return 0; }`,
		`import "std/io_buffered"; function main(): i32 { let n = io_buffered.buf_u64_len(1u64); return 0; }`,
		`import "std/io_buffered"; function main(): i32 { let digits = io_buffered.digit_pairs(); return 0; }`,
		`import "std/io_buffered"; function inspect(b: io_buffered.BufBlock): void {} function main(): i32 { return 0; }`,

		`import "std/string"; function main(): i32 { let b = string.__is_ascii_ws(32); return 0; }`,
		`import "core/utf8_scalar"; function main(): i32 { return utf8_scalar.step("a", 0, 1); }`,
		`import "core/int"; function main(): i32 { return int.__radix_digit(48); }`,
		`import "core/map"; function main(): i32 { return map.__map_pow2_ceil(3); }`,
		`import "std/json"; function main(): i32 { let x = json.from_json_field(json.JNull, "x"); return 0; }`,
		`import "std/pvec"; function inspect(n: pvec.VNode[i32]): i32 { return 0; } function main(): i32 { return 0; }`,
		`import "std/pvec"; function main(): i32 { let node = pvec.Leaf([1]); return 0; }`,
		`import "std/json"; struct Item { value: i32 } impl json.FromJson for Item { function from_json_value(v: json.JsonValue): Result[Item, string] { let field = json.from_json_field(v, "value"); return Err("manual"); } } function main(): i32 { return 0; }`,
	} {
		dir := writeFiles(t, map[string]string{"main.fern": source})
		_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
		if err == nil || !strings.Contains(err.Error(), "pub(package)") {
			t.Fatalf("got %v, want a package visibility refusal for %s", err, source)
		}
	}
	const source = `import "std/json";
import "std/pvec";
import "std/u64";
import "std/string";
import "std/utf8";
@derive(json.FromJson)
struct Item { value: i32, names: string[], note: Option[string] }
function main(): i32 {
  let item = Item.from_json("{\"value\":42,\"names\":[\"a\"]}");
  let vector: pvec.PVec[i32] = pvec.from_array([42]);
  if ((18000000000000000007 as u64).to_string() != "18000000000000000007") { return 1; }
  if (utf8.substring("éx", 0, 1) != "é") { return 2; }
  return vector.get_or(0, 0);
}`
	dir := writeFiles(t, map[string]string{"main.fern": source})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatal(err)
	}
}

func TestPublicVariantTakesPrecedenceOverPrivateVariant(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lib.fern":  `enum Hidden { Shared(i32) } pub enum Open { Shared(string) }`,
		"main.fern": `import "./lib"; function main(): i32 { let node: lib.Open = lib.Shared("ok"); match (node) { lib.Shared(s) => { return s.len(); } } return 0; }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatal(err)
	}
}
