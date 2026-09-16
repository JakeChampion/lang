package e2e

import "testing"

// The Map family on `-backend ssa -target x86-64-linux`, against the DEFAULT
// x86-64 backend on stdout, stderr and exit status. The Map is core/map.fern
// on both sides; what this backend supplies is the allocator pair, the byte
// fill, the hash seed, and the two drops, plus the call-site alias from
// `map_new` to `map_new_impl`. Iteration order depends on the per-process
// seed, so every case prints an order-independent observation.
var x86SSAMapCases = []struct {
	name string
	src  string
}{
	{
		name: "string_keys_insert_get_has_len_without_and_cleared",
		src: `import "std/i32";
import "core/map";
function main(): i32 {
  var m: Map[string, i32] = map_new(4);
  m = m.insert("one", 1);
  m = m.insert("two", 2);
  m = m.insert("three", 3);
  m = m.insert("two", 22);
  stdout().write("len=" + m.len().to_string() + "\n");
  match (m.get("two")) {
    Some(v) => { stdout().write("two=" + v.to_string() + "\n"); },
    None => { return 1; },
  }
  match (m.get("four")) {
    Some(v) => { return 2; },
    None => { stdout().write("four-absent\n"); },
  }
  stdout().write("has-one=" + m.has("one").to_string() + " get_or=" + m.get_or("nine", 9).to_string() + "\n");
  var d = m.without("one");
  m = d.0;
  stdout().write("deleted=" + d.1.to_string() + " len=" + m.len().to_string() + "\n");
  var ks: string[] = m.keys();
  var vs: i32[] = m.values();
  var total: i32 = 0;
  var i: i32 = 0;
  while (i < vs.len()) { total = total + vs[i]; i = i + 1; }
  stdout().write("keys=" + ks.len().to_string() + " values-sum=" + total.to_string() + "\n");
  m = m.cleared();
  stdout().write("cleared=" + m.len().to_string() + "\n");
  return 0;
}`,
	},
	{
		// Enough entries to grow the table several times; the sum over the
		// iterator and a lookup of every key check the rehash kept them.
		name: "i32_keys_grow_and_iterate",
		src: `import "std/i32";
import "core/map";
function main(): i32 {
  var m: Map[i32, i32] = map_new(2);
  var i: i32 = 0;
  while (i < 500) { m = m.insert(i * 7, i); i = i + 1; }
  var missing: i32 = 0;
  i = 0;
  while (i < 500) { if (m.get_or(i * 7, 0 - 1) != i) { missing = missing + 1; } i = i + 1; }
  var it = m.iter();
  var sumk: i64 = 0;
  var sumv: i64 = 0;
  var n: i32 = 0;
  while (it.has_next()) { sumk = sumk + (it.key() as i64); sumv = sumv + (it.value() as i64); n = n + 1; it.advance(); }
  stdout().write("len=" + m.len().to_string() + " missing=" + missing.to_string() + " n=" + n.to_string() + "\n");
  if (sumk == 873250 && sumv == 124750) { stdout().write("sums-ok\n"); } else { return 1; }
  return 0;
}`,
	},
	{
		// Array values are rc-tracked, so the value column is dropped
		// through __fern_drop_arr_ptr; string values through their own path.
		name: "array_and_string_values",
		src: `import "std/i32";
import "core/map";
function main(): i32 {
  var m: Map[string, i32[]] = map_new(4);
  m = m.insert("a", [1, 2, 3]);
  m = m.insert("b", [4, 5]);
  m = m.insert("a", [10, 20, 30, 40]);
  var total: i32 = 0;
  match (m.get("a")) {
    Some(xs) => { var i: i32 = 0; while (i < xs.len()) { total = total + xs[i]; i = i + 1; } },
    None => { return 1; },
  }
  stdout().write("a-sum=" + total.to_string() + " len=" + m.len().to_string() + "\n");
  var s: Map[i32, string] = map_new(4);
  s = s.insert(1, "one");
  s = s.insert(2, "two" + "!");
  stdout().write(s.get_or(2, "none") + " " + s.get_or(3, "none") + "\n");
  return 0;
}`,
	},
	{
		// Many short-lived maps: each is dropped at scope exit through
		// __fern_map_drop, and the seed is drawn once for all of them.
		name: "many_maps_in_a_loop",
		src: `import "std/i32";
import "core/map";
function count(n: i32): i32 {
  var m: Map[string, i32] = map_new(2);
  var i: i32 = 0;
  while (i < n) { m = m.insert("k" + i.to_string(), i); i = i + 1; }
  return m.len();
}
function main(): i32 {
  var total: i32 = 0;
  var r: i32 = 0;
  while (r < 300) { total = total + count(r % 17); r = r + 1; }
  stdout().write("total=" + total.to_string() + "\n");
  return 0;
}`,
	},
}

func TestX86_64SSAMapFamilyMatchesDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSAMapCases {
		t.Run(c.name, func(t *testing.T) {
			ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, c.src, true)
			flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, c.src, false)

			if ssaCode != flatCode {
				t.Errorf("exit status: ssa=%d flat=%d", ssaCode, flatCode)
			}
			if ssaOut != flatOut {
				t.Errorf("stdout differs:\n ssa=%q\nflat=%q", ssaOut, flatOut)
			}
			if ssaErr != flatErr {
				t.Errorf("stderr differs:\n ssa=%q\nflat=%q", ssaErr, flatErr)
			}
			if ssaOut == "" {
				t.Errorf("no output at all: the program did not reach its prints")
			}
		})
	}
}
