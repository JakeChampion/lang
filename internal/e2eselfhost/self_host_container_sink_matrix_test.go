package e2eselfhost

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- The container-sink matrix -----------------------------------------------
//
// The other half of the construction-retain map. That one enumerates struct
// LITERAL FIELD kinds; this one enumerates the CONTAINER positions a whole
// struct BOX can be stored into — an array-literal element, an `append` or
// `with` argument, a tuple element, a variant payload, an Option payload — x
// what the source local does afterwards.
//
// The axis that matters is the second one. A store at the source's LAST USE is
// a MOVE: the container takes the one reference and the source's release is
// elided, which every position has always handled. A source that stays LIVE (or
// is REBOUND) makes the store a counted SHARE, and that is what needs the
// store's retain and an rc-gated release on both owners. Positions whose
// release protocol is unfinished refuse the source's credit outright rather
// than dangle (struct_box_sink_kind's SINK_REFUSED), so they leak here and the
// pinned cell says so.
//
// Exit codes must match between the compilers on every cell and the underflow
// guard fails hard on either side: an over-release is a bug in any cell, listed
// or not. x86-64 only — the comparison is between compilers, not targets.

type csmCell struct {
	name string
	src  string
}

// csmDecls is shared by every cell: a struct with an rc-tracked array field (so
// the release is a real field walk, not just a box dec) and an enum wrapping it.
const csmDecls = `struct P { xs: i32[], k: i32 }
enum E { A(P), B }
`

// csmMain: 100 rounds, checksum, underflow guard — the construction matrix's
// driver, so a cell's exit code is comparable across both maps.
const csmMain = `
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// csmPosition is one container position: the statements that store `p` into it,
// and an i32 expression that reads the container back.
type csmPosition struct {
	name  string
	store string
	read  string
}

var csmPositions = []csmPosition{
	{"arrlit", `var out: P[] = [p];`, `out.len() + out[0].k`},
	{"append", `var out: P[] = [];
    out = out.append(p);`, `out.len() + out[0].k`},
	{"with", `var out: P[] = [P { xs: [i, i + 9], k: i }];
    out = out.with(0, p);`, `out.len() + out[0].k`},
	{"tuple", `var tp: (i32, P) = (i, p);`, `tp.0 + tp.1.k`},
	{"variant", `var e: E = E.A(p);`, `(match (e) { E.A(q) => q.k, E.B => 0 })`},
	{"option", `var o: Option[P] = Some(p);`, `(match (o) { Some(q) => q.k, None => 0 })`},
}

// csmRound wraps a body + read expression in the shared round/main driver.
func csmRound(body, read string) string {
	return csmDecls + "\nfunction round(i: i32): i32 {\n" + body +
		"    return " + read + ";\n}\n" + csmMain
}

func containerSinkCells() []csmCell {
	var cells []csmCell
	for _, pos := range csmPositions {
		body := "    var p: P = P { xs: [i, i + 1], k: i };\n    " + pos.store + "\n"
		// moved: the store is p's last mention, so it TRANSFERS the reference.
		cells = append(cells, csmCell{pos.name + "__moved", csmRound(body, pos.read)})
		// live: p's own field is read afterwards, so the store is a counted SHARE
		// and both owners must release under the rc gate.
		cells = append(cells, csmCell{pos.name + "__live", csmRound(body, pos.read+" + p.xs[0]")})
	}
	// rebound: the source is superseded between two stores, so the first store
	// is a share whose release the rebind owes and the second is a move.
	cells = append(cells, csmCell{"arrlit__rebound", csmRound(
		`    var p: P = P { xs: [i, i + 1], k: i };
    var a: P[] = [p];
    p = P { xs: [i + 2, i + 3], k: i + 1 };
    var b: P[] = [p];
`, "a[0].k + b[0].xs[0]")})
	cells = append(cells, csmCell{"append__rebound", csmRound(
		`    var out: P[] = [];
    var p: P = P { xs: [i, i + 1], k: i };
    out = out.append(p);
    p = P { xs: [i + 2, i + 3], k: i + 1 };
    out = out.append(p);
`, "out.len() + out[0].k + out[1].xs[0]")})
	// blockscoped: retire_locals renames the source at block exit, which is the
	// axis deciding which credit predicate the retain must read.
	cells = append(cells, csmCell{"append__blockscoped", csmRound(
		`    var out: P[] = [];
    var j: i32 = 0;
    while (j < 2) {
        var p: P = P { xs: [i + j, i + j + 1], k: i + j };
        out = out.append(p);
        j = j + 1;
    }
`, "out.len() + out[0].k + out[1].xs[0]")})
	return cells
}

func loadContainerSinkMatrix(t *testing.T) map[string][2]leakVerdict {
	t.Helper()
	path := filepath.Join("testdata", "selfhost-container-sink-matrix.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string][2]leakVerdict{}
	sc := bufio.NewScanner(f)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("%s:%d: want `<cell> <native> <selfhost> <note>`, got %q", path, ln, line)
		}
		out[fields[0]] = [2]leakVerdict{leakVerdict(fields[1]), leakVerdict(fields[2])}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

func TestSelfHostContainerSinkMatrixX86_64(t *testing.T) {
	// CI-DARK: FERN_CONTAINER_SINK_DUMP — a regeneration tool, not coverage: it
	// prints measured matrix-file lines INSTEAD of comparing, so a lane setting
	// it would disable this gate. The compare path below is the CI behaviour.
	dump := os.Getenv("FERN_CONTAINER_SINK_DUMP") == "1"
	var known map[string][2]leakVerdict
	if !dump {
		known = loadContainerSinkMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cells := containerSinkCells()
	seen := map[string]bool{}
	for _, cell := range cells {
		seen[cell.name] = true
		t.Run(cell.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdict(t, cli, dir, "csm_"+cell.name, cell.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, "csm_"+cell.name, cell.src)

			if dump {
				fmt.Printf("%-28s %-6s %-6s (exit native=%d selfhost=%d)\n",
					cell.name, natV, shV, natExit, shExit)
				return
			}
			if natExit == 99 || shExit == 99 {
				t.Fatalf("underflow guard tripped (native=%d self-host=%d): an "+
					"over-release, which no matrix entry may pin", natExit, shExit)
			}
			if shV == verdictCrash {
				t.Errorf("self-host binary crashed (exit %d):\n%s", shExit, cell.src)
				return
			}
			if natV != verdictError && shV != verdictError && natExit != shExit {
				t.Errorf("exit codes disagree: native=%d self-host=%d — a wrong-code "+
					"divergence, not a matrix update:\n%s", natExit, shExit, cell.src)
				return
			}
			want, ok := known[cell.name]
			if !ok {
				t.Errorf("cell %s not in the pinned matrix — run with "+
					"FERN_CONTAINER_SINK_DUMP=1 and add the measured line", cell.name)
				return
			}
			if natV != want[0] || shV != want[1] {
				t.Errorf("%s: measured native=%s selfhost=%s, pinned native=%s selfhost=%s",
					cell.name, natV, shV, want[0], want[1])
			}
		})
	}
	if !dump {
		for name := range known {
			if !seen[name] {
				t.Errorf("pinned cell %s no longer generated", name)
			}
		}
	}
}
