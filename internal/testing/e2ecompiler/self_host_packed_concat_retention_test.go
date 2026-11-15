package e2ecompiler

import (
	"strings"
	"testing"
)

const packedConcatRetentionProgram = `import "std/string";
enum Item { Empty, Text(u8, boolean, i32, string), Pull(u8, () => string) }
@noinline function capture(text: string): () => string { return (): string => text; }
@noinline function pack(view: string, n: i32): Item[] {
  return [Text(231u8, true, n, view), Pull(17u8, capture(view)), Empty];
}
@noinline function source(unit: string, n: i32): Item[] {
  let backing = unit.repeat(12);
  return pack(slice_unchecked(backing, 2, 10), n);
}
@noinline function cat_items(own dst: Item[], src: [Item], lo: i32, hi: i32): Item[] {
  for i in lo..hi { dst = dst.append(src[i]); }
  return dst;
}
@noinline function check(items: Item[], at: i32, want: string, number: i32): i32 {
  match (items[at]) {
    Text(tag, flag, n, text) => {
      if (tag != 231u8 || !flag || n != number || text != want) { return 11; }
    },
    _ => { return 12; },
  }
  match (items[at + 1]) {
    Pull(tag, get) => { if (tag != 17u8 || get() != want) { return 13; } },
    _ => { return 14; },
  }
  match (items[at + 2]) { Empty => {}, _ => { return 15; } }
  return 0;
}
@noinline function churn(): i32 {
  let total = 0;
  for i in 0..128 { let junk = ("zq" + i.to_string()).repeat(8); total += junk.len(); }
  return total;
}
function main(): i32 {
  let base = source("xy", -2147483648);
  let kept = base;
  let src = source("ab", 2147483647);
  let out = cat_items(base, src, 0, src.len());
  let alias = out;
  base = [];
  src = [];
  if (out.len() != 6 || kept.len() != 3 || churn() <= 0) { return 21; }
  if (check(out, 0, "xyxyxyxy", -2147483648) != 0) { return 22; }
  if (check(out, 3, "abababab", 2147483647) != 0) { return 23; }
  if (check(kept, 0, "xyxyxyxy", -2147483648) != 0) { return 24; }
  let grown: Item[] = [];
  for j in 0..24 { grown = cat_items(grown, out, 0, out.len()); }
  out = [];
  if (grown.len() != 144 || alias.len() != 6 || churn() <= 0) { return 25; }
  if (check(grown, 138, "xyxyxyxy", -2147483648) != 0) { return 26; }
  if (check(grown, 141, "abababab", 2147483647) != 0) { return 27; }
  let none = cat_items(grown, alias, 5, 2);
  grown = [];
  if (none.len() != 144 || check(alias, 3, "abababab", 2147483647) != 0) { return 28; }
  kept = [];
  alias = [];
  if (churn() <= 0) { return 30; }
  if (check(none, 138, "xyxyxyxy", -2147483648) != 0) { return 31; }
  if (check(none, 141, "abababab", 2147483647) != 0) { return 32; }
  if (__rc_underflow_count() != 0) { return 29; }
  print("packed concat views survive");
  return 0;
}
`

func TestSelfHostPackedConcatRetention(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const want = "packed concat views survive\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			got, stderr := runLentView(t, cli, packedConcatRetentionProgram, target, true)
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			assertBalancedCensus(t, stderr)
			t.Logf("%s", leakSummaryLine(stderr))
			body := emittedFunction(t, cli, packedConcatRetentionProgram, target, "cat_items")
			if !strings.Contains(body, "__fern_arr_concat") || hasLoop(body, target) {
				t.Errorf("cat_items does not use one concat kernel:\n%s", body)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target+"-harden", func(t *testing.T) {
			bin := compileCPUProfile(t, cli, packedConcatRetentionProgram, target, "", "-harden", "-sanitize")
			cmd := runX86_64Bin(cli.runner, bin)
			if target == "arm64-linux" {
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, bin)
			}
			got, stderr := runCPUProfile(t, cmd, 0)
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			assertBalancedCensus(t, stderr)
			t.Logf("%s", leakSummaryLine(stderr))
		})
	}
}
