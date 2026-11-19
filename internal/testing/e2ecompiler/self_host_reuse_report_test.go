package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const selfHostReuseReportSrc = `enum List { Cons(i32, List), Nil }
enum Opt { Has(i32), None }
struct Rec { a: i32, b: string, c: List, d: i32 }

@noinline
function inc(xs: List): List {
  match (xs) {
    Cons(h, t) => { return Cons(h + 1, inc(t)); },
    Nil => { return Nil; },
  }
}

@noinline
function push(xs: List, n: i32): List {
  return Cons(n, xs);
}

@noinline
function bump(own r: Rec): Rec {
  return Rec { ...r, a: r.a + 1 };
}

@noinline
function pair(xs: List): (i32, List) {
  match (xs) {
    Cons(h, t) => { return (h, t); },
    Nil => { return (0, Nil); },
  }
}

@noinline
function put(own xs: i32[], i: i32): i32[] {
  return xs.with(i, 9);
}

@noinline
function put_live(n: i32): i32 {
  let xs: i32[] = [1, 2, 3];
  let keep: i32[] = xs;
  xs = xs.with(n, 9);
  return xs[n] + keep[n];
}

@noinline
function put_sole(n: i32): i32 {
  let xs: i32[] = [1, 2, 3];
  xs = xs.with(n, 9);
  return xs[n];
}

function main(): i32 {
  let l: List = inc(push(push(Nil, 1), 2));
  let r: Rec = bump(Rec { a: 1, b: "b", c: l, d: 4 });
  let p: (i32, List) = pair(r.c);
  let a: i32[] = put([1, 2, 3], 1);
  return r.a + p.0 + a[1] + put_live(0) + put_sole(2) - 33;
}
`

// The rows of one section of the report, by the function that owns each row.
func reuseReportRows(t *testing.T, report, kind, plan string) map[string][]string {
	t.Helper()
	header := "array report: primary Fern compiler, " + kind + "\n"
	start := strings.Index(report, header)
	if start < 0 {
		t.Fatalf("missing %s section:\n%s", kind, report)
	}
	body, _, _ := strings.Cut(report[start+len(header):], kind+" decisions: ")
	lines := strings.Split(body, "\n")
	rows := map[string][]string{}
	for i := 0; i+1 < len(lines); i++ {
		if owner, ok := strings.CutSuffix(lines[i], ": "+plan+"; ownership lowering"); ok {
			rows[owner] = append(rows[owner], lines[i+1])
		}
	}
	return rows
}

// The report names each `.with` and each construction of a variant, a record
// or a tuple, and says where its box comes from. A construction that gets
// the box of a dropped value names that value.
func TestSelfHostReuseReport(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostReuseReportSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(env ...string) (string, string) {
		t.Helper()
		asm := filepath.Join(t.TempDir(), "out.s")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", "x86-64-linux", "-emit", "asm", "-o", asm, src, cli.stdlib)
		cmd.Env = append(append(os.Environ(), "FERN_ARRAY_REPORT=1"), env...)
		msg, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compile %v: %v\n%s", env, err, msg)
		}
		body, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		return string(body), string(msg)
	}
	type site struct {
		owner string
		want  []string
	}
	check := func(report, kind, plan string, sites []site) {
		t.Helper()
		rows := reuseReportRows(t, report, kind, plan)
		for _, tc := range sites {
			found := false
			for _, row := range rows[tc.owner] {
				all := true
				for _, part := range tc.want {
					all = all && strings.Contains(row, part)
				}
				found = found || all
			}
			if !found {
				t.Errorf("%s: no %s row with %q in %q", tc.owner, kind, tc.want, rows[tc.owner])
			}
		}
	}
	asm, report := compile()
	check(report, "constructor storage", "constructor", []site{
		{"inc", []string{"stage=Cons@", " donor=v", " reason=reuse storage=unique-in-place/shared-fresh"}},
		{"inc", []string{"stage=Nil@", " reason=static storage=static"}},
		{"push", []string{"stage=Cons@", " line=", " reason=no-donor storage=fresh"}},
		{"bump", []string{"stage=Rec@", " donor=v0 (r)", " reason=reuse storage=unique-in-place/shared-fresh"}},
		{"main", []string{"stage=Rec@", " reason=no-donor storage=fresh"}},
	})
	check(report, "with storage", "with", []site{
		{"put", []string{"stage=with@", " (xs)", " line=", " reason=guarded storage=unique-in-place/shared-copy"}},
	})
	if out, err := runX86_64Bin(cli.runner, buildBin(t, cli.gcc, t.TempDir(), "reuse-report", asm)).CombinedOutput(); err != nil {
		t.Fatalf("reported program: %v\n%s", err, out)
	}

	_, off := compile("FERN_SELFHOST_NO_REUSE=1")
	check(off, "constructor storage", "constructor", []site{
		{"inc", []string{"stage=Cons@", " donor=v", " reason=reuse-disabled storage=fresh"}},
		{"bump", []string{"stage=Rec@", " reason=reuse-disabled storage=fresh"}},
		{"push", []string{"stage=Cons@", " reason=no-donor storage=fresh"}},
	})
}
