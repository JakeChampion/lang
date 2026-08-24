package e2eselfhost

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- The construction-retain matrix ------------------------------------------
//
// The killer-drops-fields wave's map. The struct release helpers' field decs
// are rc-GUARDED (__fern_arr_dec / __fern_str_free / __fern_str_arr_free all
// dec-or-free by rc), so a release is unsound only when a SHARE of the field
// went UNCOUNTED — the flatten__RewriteCtx wasm fault was an unbalanced dec
// from an un-retained string[] field READ, not a blind walk. This matrix
// enumerates struct-literal FIELD KIND x VALUE SHAPE and pins, per cell,
// whether each compiler ends census-clean — so every retain hole the wave
// closes flips a recorded cell deliberately, and a regression flips a clean
// cell loudly. Exit codes must match between compilers on every cell, and the
// underflow guard fails hard on either side: an over-release (an uncounted
// share whose release fired anyway) is a bug in any cell, listed or not.
//
// Shapes: fresh (literal/ctor inline — sole-owned, the no-inc baseline),
// local (bare ident of a fresh local, read after the holder dies), param
// (bare ident of a param the CALLER keeps live), fieldread (`q.f` off a live
// sibling struct), call (a producer's result). x86-64 only — the comparison
// is between compilers, not targets (the leak matrix states the same rule).

type crmCell struct {
	name string
	src  string
}

// crmMain is the shared driver: 100 rounds, checksum, underflow guard.
const crmMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// crmMainKeep is the param-shape driver: main builds the source ONCE, keeps
// it live across every call, and reads it after the loop — the same two
// origin-probe conditions the leak matrix's alias_param cells state.
func crmMainKeep(keepInit, keepRead string) string {
	return `function main(): i32 {
    ` + keepInit + `
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(keep, r); t = t + 0; r = r + 1; }
    t = (t + ` + keepRead + `) % 97;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`
}

func constructionRetainCells() []crmCell {
	// Per-kind pieces. Every constructed value embeds the loop variable so
	// neither pipeline const-folds it (the leak matrix's #7364 trap).
	type kind struct {
		name  string
		decls string // aux type + producer decls (P is added per shape)
		field string // P's field type
		fresh string // inline fresh value, uses `i`
		mk    string // producer fn returning the field type, uses arg `i`
		read  string // i32 expression over `p.f`
	}
	kinds := []kind{
		{
			name: "str", field: "string",
			decls: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }`,
			fresh: `w("f")`, mk: "mkv", read: "p.f.len()",
		},
		{
			name: "str_arr", field: "string[]",
			decls: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { var o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }`,
			fresh: `[w("x"), w("y")]`, mk: "mkv", read: "p.f.len() + p.f[0].len()",
		},
		{
			name: "arr_i32", field: "i32[]",
			decls: `function mkv(i: i32): i32[] { var o: i32[] = [i, i + 1]; return o; }`,
			fresh: `[i, i + 2]`, mk: "mkv", read: "p.f.len() + p.f[0]",
		},
		{
			name: "struct", field: "Inner",
			decls: `struct Inner { xs: i32[], k: i32 }
function mkv(i: i32): Inner { return Inner { xs: [i, i + 1], k: i }; }`,
			fresh: `Inner { xs: [i, i + 3], k: i }`, mk: "mkv", read: "p.f.xs.len() + p.f.k",
		},
		{
			name: "enum", field: "E",
			decls: `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }`,
			fresh: `E.A([i, i + 4])`, mk: "mkv",
			read: `(match (p.f) { E.A(xs) => xs.len(), E.B => 0 })`,
		},
		{
			name: "enum_arr", field: "E[]",
			decls: `enum E { A(i32[]), B }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }`,
			fresh: `[E.A([i, i + 5])]`, mk: "mkv", read: "p.f.len()",
		},
		{
			name: "struct_arr", field: "Inner[]",
			decls: `struct Inner { xs: i32[], k: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }`,
			fresh: `[Inner { xs: [i, i + 6], k: i }]`, mk: "mkv", read: "p.f.len() + p.f[0].k",
		},
	}

	var cells []crmCell
	for _, k := range kinds {
		holder := "struct P { f: " + k.field + ", n: i32 }\n"
		decls := holder + k.decls + "\n"

		// fresh: inline construction, sole-owned by the holder.
		cells = append(cells, crmCell{
			name: k.name + "__fresh",
			src: decls + `function round(i: i32): i32 {
    var p: P = P { f: ` + k.fresh + `, n: i };
    return (` + k.read + ` + p.n) % 101;
}
` + crmMain,
		})

		// local: bare ident of a fresh local; the source is read AFTER the
		// holder's last use, so both claims are live across the bind.
		cells = append(cells, crmCell{
			name: k.name + "__local",
			src: decls + `function round(i: i32): i32 {
    var src: ` + k.field + ` = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) {
        var p: P = P { f: src, n: i };
        t = (` + k.read + ` + p.n) % 101;
    }
    var q: P = P { f: mkv(i + 1), n: i };
    var p: P = q;
    t = (t + p.n) % 101;
    return t;
}
` + crmMain,
		})

		// param: the caller keeps the value across every call and reads it
		// after the loop — an over-release here dangles main's copy.
		cells = append(cells, crmCell{
			name: k.name + "__param",
			src: decls + `function round(src: ` + k.field + `, i: i32): i32 {
    var p: P = P { f: src, n: i };
    return (` + k.read + ` + p.n) % 101;
}
` + crmMainKeep("var keep: "+k.field+" = mkv(7);", "0"),
		})

		// fieldread: `q.f` off a live sibling holder — the RewriteCtx shape.
		// A ONCE bind, deliberately: a LOOP-carried rebind of the same field
		// is a different axis (the rebind's cow skip strands per-iteration
		// retains — load-bearing for spread carries, #6653) and would mask
		// whether the READ itself is counted.
		cells = append(cells, crmCell{
			name: k.name + "__fieldread",
			src: decls + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i };
    return (` + k.read + ` + p.n + q.n) % 101;
}
` + crmMain,
		})

		// call: the producer's result straight into the field.
		cells = append(cells, crmCell{
			name: k.name + "__call",
			src: decls + `function round(i: i32): i32 {
    var p: P = P { f: mkv(i), n: i };
    return (` + k.read + ` + p.n) % 101;
}
` + crmMain,
		})
	}
	return cells
}

func loadConstructionRetainMatrix(t *testing.T) map[string][2]leakVerdict {
	t.Helper()
	path := filepath.Join("testdata", "selfhost-construction-retain-matrix.txt")
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

func TestSelfHostConstructionRetainMatrixX86_64(t *testing.T) {
	// CI-DARK: FERN_CONSTRUCTION_RETAIN_DUMP — a regeneration tool, not
	// coverage: it prints measured matrix-file lines INSTEAD of comparing, so
	// a lane setting it would disable this gate. The compare path below is
	// the CI behaviour.
	dump := os.Getenv("FERN_CONSTRUCTION_RETAIN_DUMP") == "1"
	var known map[string][2]leakVerdict
	if !dump {
		known = loadConstructionRetainMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cells := constructionRetainCells()
	seen := map[string]bool{}
	for _, cell := range cells {
		seen[cell.name] = true
		t.Run(cell.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdict(t, cli, dir, "crm_"+cell.name, cell.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, "crm_"+cell.name, cell.src)

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
					"FERN_CONSTRUCTION_RETAIN_DUMP=1 and add the measured line", cell.name)
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
