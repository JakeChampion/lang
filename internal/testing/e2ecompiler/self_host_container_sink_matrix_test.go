// CI-DARK: FERN_CONTAINER_SINK_DUMP — This flag regenerates the reference data.
package e2ecompiler

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

type csmCell struct {
	name string
	src  string
}

const csmDecls = `struct P { xs: i32[], k: i32 }
enum E { A(P), B }
`

const csmMain = `
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

type csmPosition struct {
	name  string
	store string
	read  string
}

var csmPositions = []csmPosition{
	{"arrlit", `let out: P[] = [p];`, `out.len() + out[0].k`},
	{"append", `let out: P[] = [];
    out = out.append(p);`, `out.len() + out[0].k`},
	{"with", `let out: P[] = [P { xs: [i, i + 9], k: i }];
    out = out.with(0, p);`, `out.len() + out[0].k`},
	{"tuple", `let tp: (i32, P) = (i, p);`, `tp.0 + tp.1.k`},
	{"variant", `let e: E = E.A(p);`, `(match (e) { E.A(q) => q.k, E.B => 0 })`},
	{"variant_unqual", `let e: E = A(p);`, `(match (e) { E.A(q) => q.k, E.B => 0 })`},
	{"option", `let o: Option[P] = Some(p);`, `(match (o) { Some(q) => q.k, None => 0 })`},
	{"option_stmt", `let o: Option[P] = Some(p);
    let n: i32 = 0;
    match (o) { Some(q) => { n = q.k; }, None => { n = 0; } }`, `n`},
	{"result_stmt", `let o: Result[P, i32] = Ok(p);
    let n: i32 = 0;
    match (o) { Ok(q) => { n = q.k; }, Err(e) => { n = 0; } }`, `n`},
}

func csmRound(body, read string) string {
	return csmDecls + "\nfunction round(i: i32): i32 {\n" + body +
		"    return " + read + ";\n}\n" + csmMain
}

func containerSinkCells() []csmCell {
	var cells []csmCell
	for _, pos := range csmPositions {
		body := "    let p: P = P { xs: [i, i + 1], k: i };\n    " + pos.store + "\n"
		cells = append(cells, csmCell{pos.name + "__moved", csmRound(body, pos.read)})
		cells = append(cells, csmCell{pos.name + "__live", csmRound(body, pos.read+" + p.xs[0]")})
	}
	cells = append(cells, csmCell{"arrlit__rebound", csmRound(
		`    let p: P = P { xs: [i, i + 1], k: i };
    let a: P[] = [p];
    p = P { xs: [i + 2, i + 3], k: i + 1 };
    let b: P[] = [p];
`, "a[0].k + b[0].xs[0]")})
	cells = append(cells, csmCell{"append__rebound", csmRound(
		`    let out: P[] = [];
    let p: P = P { xs: [i, i + 1], k: i };
    out = out.append(p);
    p = P { xs: [i + 2, i + 3], k: i + 1 };
    out = out.append(p);
`, "out.len() + out[0].k + out[1].xs[0]")})
	cells = append(cells, csmCell{"append__blockscoped", csmRound(
		`    let out: P[] = [];
    let j: i32 = 0;
    while (j < 2) {
        let p: P = P { xs: [i + j, i + j + 1], k: i + j };
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
	dump := envflag.On("FERN_CONTAINER_SINK_DUMP")
	var known map[string][2]leakVerdict
	if !dump {
		known = loadContainerSinkMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

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
