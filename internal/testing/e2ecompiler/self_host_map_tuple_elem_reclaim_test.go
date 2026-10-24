package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func mapTupleElemChurn(prelude, body string, rounds int) string {
	return fmt.Sprintf(`import "core/map";
%sfunction churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
%s
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return churn(%d) + __rc_underflow_count() * 100; }
`, prelude, body, rounds)
}

var mapTupleElemFlatCases = []struct {
	name    string
	prelude string
	body    string
	want100 int
	want200 int
}{
	{
		name: "basic",
		body: `        let m: Map[string, i32] = map_new(4);
        let t: (i32, Map[string, i32]) = (i, m);
        acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		name: "pos0",
		body: `        let m: Map[string, i32] = map_new(4);
        let t: (Map[string, i32], i32) = (m, i);
        acc = (acc + t.1 + t.0.len()) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		name: "two_maps",
		body: `        let a: Map[string, i32] = map_new(4);
        let b: Map[string, i32] = map_new(4);
        let t: (Map[string, i32], Map[string, i32]) = (a, b);
        acc = (acc + i + t.0.len() + t.1.len()) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		name: "same_map_twice",
		body: `        let m: Map[string, i32] = map_new(4);
        let t: (Map[string, i32], Map[string, i32]) = (m, m);
        acc = (acc + i + t.0.len() + t.1.len()) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		name: "arr_and_map",
		body: `        let m: Map[string, i32] = map_new(4);
        let t: (i32[], Map[string, i32]) = ([i, i + 1], m);
        acc = (acc + t.0.len() + t.1.len()) % 91;`,
		want100: 18,
		want200: 36,
	},
	{
		name: "if_host",
		body: `        if (i % 2 == 0) {
            let m: Map[string, i32] = map_new(4);
            let t: (i32, Map[string, i32]) = (i, m);
            acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;
        } else { acc = (acc + 1) % 91; }`,
		want100: 43,
		want200: 81,
	},
	{
		name: "match_host",
		body: `        let o: Option[i32] = Some(i);
        match (o) {
            Some(v) => {
                let m: Map[string, i32] = map_new(4);
                let t: (i32, Map[string, i32]) = (v, m);
                acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;
            },
            None => { acc = acc + 1; }
        }`,
		want100: 36,
		want200: 62,
	},
	{
		name:    "borrow_call_arg",
		prelude: "function tlen(mm: Map[string, i32]): i32 { return mm.len(); }\n",
		body: `        let m: Map[string, i32] = map_new(4);
        let t: (i32, Map[string, i32]) = (i, m);
        acc = (acc + t.0 + tlen(m)) % 91;`,
		want100: 36,
		want200: 62,
	},
}

var mapTupleElemHazardCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "payload_returned",
		src: `import "core/map";
function pick(i: i32): Map[string, i32] {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    let t: (i32, Map[string, i32]) = (i, m);
    return t.1;
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let g: Map[string, i32] = pick(i); acc = (acc + g.get_or("k", 0)) % 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(60) + __rc_underflow_count() * 100; }
`,
		want: 41,
	},
	{
		name: "payload_extracted_escapes",
		src: `import "core/map";
function pick(i: i32): Map[string, i32] {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    let t: (i32, Map[string, i32]) = (i, m);
    let keep: Map[string, i32] = t.1;
    return keep;
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let g: Map[string, i32] = pick(i); acc = (acc + g.get_or("k", 0)) % 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(60) + __rc_underflow_count() * 100; }
`,
		want: 41,
	},
	{
		name: "payload_extracted_local",
		src: mapTupleElemChurn("", `        let m: Map[string, i32] = map_new(4);
        let t: (i32, Map[string, i32]) = (i, m);
        let keep: Map[string, i32] = t.1;
        acc = (acc + t.0 + keep.len()) % 91;`, 60),
		want: 41,
	},
	{
		name: "identity_through_tuple",
		src: mapTupleElemChurn("", `        let m: Map[string, i32] = map_new(4);
        let t: (i32, Map[string, i32]) = (i, m);
        let mm: Map[string, i32] = t.1.insert("k", i);
        acc = (acc + t.0 + mm.get_or("k", 0)) % 91;`, 60),
		want: 82,
	},
	{
		name: "tuple_returned",
		src: `import "core/map";
function mk(i: i32): (i32, Map[string, i32]) {
    let m: Map[string, i32] = map_new(4);
    let t: (i32, Map[string, i32]) = (i, m);
    return t;
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let p: (i32, Map[string, i32]) = mk(i); acc = (acc + p.0 + p.1.len()) % 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(60) + __rc_underflow_count() * 100; }
`,
		want: 41,
	},
	{
		name: "second_alias",
		src: mapTupleElemChurn("", `        let m: Map[string, i32] = map_new(4);
        let t: (i32, Map[string, i32]) = (i, m);
        let q: Map[string, i32] = m;
        acc = (acc + t.0 + q.len()) % 91;`, 60),
		want: 41,
	},
	{
		name: "unannotated_host",
		src: mapTupleElemChurn("", `        let m: Map[string, i32] = map_new(4);
        let t = (i, m);
        acc = (acc + t.0 + t.1.len()) % 91;`, 60),
		want: 41,
	},
	{
		name: "host_reassigned",
		src: mapTupleElemChurn("", `        let m: Map[string, i32] = map_new(4);
        let t: (i32, Map[string, i32]) = (i, m);
        t = (i + 1, m);
        acc = (acc + t.0 + t.1.len()) % 91;`, 60),
		want: 10,
	},
	{
		name: "map_iterated",
		src: mapTupleElemChurn("", `        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        let t: (i32, Map[string, i32]) = (i, m);
        for (kk, vv) in m { acc = (acc + vv) % 91; }
        acc = (acc + t.0) % 91;`, 60),
		want: 82,
	},
	{
		name: "for_host_box_residue",
		src: mapTupleElemChurn("", `        for k in 0 .. 2 {
            let m: Map[string, i32] = map_new(4);
            let t: (i32, Map[string, i32]) = (i + k, m);
            acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;
        }`, 200),
		want: 51,
	},
}

func TestSelfHostMapTupleElemReclaimX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	for _, tc := range mapTupleElemFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			check := func(rounds, want int) {
				t.Helper()
				src := mapTupleElemChurn(tc.prelude, tc.body, rounds)
				asm := cli.emit(t, "x86-64-linux", src, "FERN_LEAKCHECK=1")
				name := fmt.Sprintf("maptupelem_%s_%d", tc.name, rounds)
				stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, dir, name, asm))
				if exit != want {
					t.Fatalf("%s exited %d, want %d (a value >= 100 is an rc over-release, not a leak)",
						name, exit, want)
				}
				summary := ""
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, "leakcheck: ") {
						summary = line
					}
				}
				if summary == "" {
					t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
				}
				var allocs, frees, l int64
				if _, err := fmtSscan(summary, &allocs, &frees, &l); err != nil {
					t.Fatalf("%s: parse %q: %v", name, summary, err)
				}
				if allocs == 0 {
					t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
				}
				if allocs != frees || l != 0 {
					t.Errorf("%s leaks: %s. Nothing releases the mapbox — construction takes no "+
						"rc_inc and the tuple child-drops skip a bare ident, so the local owes the "+
						"one release (#7212)", name, summary)
				}
			}
			check(100, tc.want100)
			check(200, tc.want200)
		})
	}
}

func TestSelfHostMapTupleElemHazardsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapTupleElemHazardCases {
		t.Run(tc.name, func(t *testing.T) {
			if exit, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); exit != tc.want {
				t.Errorf("exited %d, want %d — %d+100 would be an rc over-release, and a "+
					"crash means the mapbox was freed under a second owner", exit, tc.want, tc.want)
			}
		})
	}
}

func mapTupleElemAllCases() []struct {
	name string
	src  string
	want int
} {
	var out []struct {
		name string
		src  string
		want int
	}
	for _, tc := range mapTupleElemFlatCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, mapTupleElemChurn(tc.prelude, tc.body, 200), tc.want200})
	}
	for _, tc := range mapTupleElemHazardCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, tc.src, tc.want})
	}
	return out
}

func TestSelfHostMapTupleElemReclaimArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapTupleElemAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}

func TestSelfHostMapTupleElemReclaimWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapTupleElemAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}
