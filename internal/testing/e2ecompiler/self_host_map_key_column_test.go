package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var noColumnMapKeyCases = []struct {
	name   string
	src    string
	typed  string
	oracle int
}{
	{"construct-only", `import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(2);
    return 7;
}
`, "ir", 7},
	{"construct-and-insert", `import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(2);
    m = m.insert(7, 3);
    return m.len() + 7;
}
`, "ir", 8},
	{"pair-iteration-of-a-parameter", `import "core/map";
function total(m: Map[i64, i32]): i32 {
    let s: i32 = 0;
    for (k, v) in m { s = s + v; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, "ir", 7},
	{"keys-of-a-parameter", `import "core/map";
function total(m: Map[u64, i32]): i32 {
    let s: i32 = 0;
    for k in m.keys() { s = s + 1; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, "ir", 7},
	{"method-on-a-parameter", `import "core/map";
function total(m: Map[usize, i32]): i32 { return m.len() + 7; }
function main(): i32 { return total(map_new(2)); }
`, "ir", 7},
	{"pair-iteration-of-an-i32-parameter", `import "core/map";
function total(m: Map[i32, i32]): i32 {
    let s: i32 = 0;
    for (k, v) in m { s = s + v; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, "ir", 7},
}

func TestSelfHostMapKeyWithNoColumnRefusesEverySurface(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host driver runs natively only")
	}
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range noColumnMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "wide_map_key_"+strings.ReplaceAll(tc.name, "-", "_")+".fern")
			if err := os.WriteFile(entry, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			if _, got := runFixtureInterp(t, entry, ""); got != tc.oracle {
				t.Fatalf("interpreter = %d, want %d (the case stopped exercising what it describes)", got, tc.oracle)
			}

			route, _ := exec.Command(driver, entry, root, "-decide").Output()
			if got := strings.TrimSpace(string(route)); got != tc.typed {
				t.Errorf("-decide = %q, want %q from the typed lowering", got, tc.typed)
			}
		})
	}
}

var narrowMapKeyCases = []struct {
	name   string
	src    string
	oracle int
}{
	{"u8", `import "core/map";
function main(): i32 {
    let m: Map[u8, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 12) { m = m.insert(i as u8, i * 2); i = i + 1; }
    let s: i32 = 0;
    for (k, v) in m { s = s + (k as i32) + v; }
    return s + m.len();
}
`, 210},
	{"u32", `import "core/map";
function main(): i32 {
    let m: Map[u32, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 12) { m = m.insert(i as u32, i * 2); i = i + 1; }
    let s: i32 = 0;
    for k in m.keys() { s = s + m.get_or(k, 0); }
    return s + m.len();
}
`, 144},
	{"boolean-literal", `import "core/map";
function main(): i32 {
    let m = Map { true: 5, false: 9 };
    let s: i32 = m.get_or(true, 0) + m.get_or(false, 0);
    if (m.contains(true)) { s = s + 1; }
    if (!m.contains(false)) { s = s + 100; }
    return s + m.len();
}
`, 17},
	{"boolean-literal-absent-key", `import "core/map";
function main(): i32 {
    let m = Map { true: 5 };
    let s: i32 = m.get_or(true, 0) + m.get_or(false, 0);
    if (!m.contains(false)) { s = s + 100; }
    return s + m.len();
}
`, 106},
	{"char-annotated", `import "core/map";
function main(): i32 {
    let m: Map[char, i32] = map_new(2);
    m = m.insert('a', 5);
    m = m.insert('\u{1F600}', 9);
    m = m.insert('a', 6);
    let s: i32 = m.get_or('a', 0) + m.get_or('\u{1F600}', 0) + m.get_or('z', 0);
    if (!m.contains('z')) { s = s + 100; }
    return s + m.len();
}
`, 117},
	{"char-literal", `import "core/map";
function main(): i32 {
    let m = Map { 'x': 5, 'y': 9 };
    let s: i32 = m.get_or('x', 0) + m.get_or('y', 0) + m.get_or('z', 0);
    if (!m.contains('z')) { s = s + 100; }
    return s + m.len();
}
`, 116},
	{"boolean-annotated", `import "core/map";
function main(): i32 {
    let m: Map[boolean, i32] = map_new(2);
    m = m.insert(true, 5);
    m = m.insert(false, 9);
    let s: i32 = m.get_or(true, 0) + m.get_or(false, 0);
    if (m.contains(true)) { s = s + 1; }
    return s + m.len();
}
`, 17},
	{"boolean-annotated-literal", `import "core/map";
function main(): i32 {
    let m: Map[boolean, i32] = Map { true: 5, false: 9 };
    return m.get_or(false, 0) + m.get_or(true, 0) + m.len();
}
`, 16},
}

func TestSelfHostNarrowMapKeyAnswersX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI runs natively only (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, tc := range narrowMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := filepath.Join(dir, "narrow_map_key_"+tc.name+".fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			if _, got := runFixtureInterp(t, prog, ""); got != tc.oracle {
				t.Fatalf("interpreter = %d, want %d (the case stopped exercising what it describes)", got, tc.oracle)
			}

			asmPath := filepath.Join(dir, "narrow_map_key_"+tc.name+".s")
			if out, err := exec.Command(cli, "-target", "x86-64-linux", "-emit", "asm",
				"-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile failed: %v\n%s", err, out)
			}
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatalf("read asm: %v", err)
			}
			bin := buildBin(t, gcc, dir, "narrow_map_key_"+tc.name, string(asm))
			run := exec.Command(bin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.oracle {
				if code < 0 {
					t.Fatalf("the self-host binary was killed by a signal (%s) — a %s key is being read "+
						"as a pointer again; the interpreter answers %d",
						run.ProcessState.String(), tc.name, tc.oracle)
				}
				t.Fatalf("self-host answered %d, interpreter says %d", code, tc.oracle)
			}
		})
	}
}

const monomorphisedFloatKeySrc = `import "core/map";
function build[T](k: T, v: i32): Map[T, i32] {
    let m: Map[T, i32] = map_new(2);
    return m.insert(k, v);
}
function main(): i32 { return build(2.5, 7).get_or(2.5, 0); }
`

func TestSelfHostMonomorphisedFloatKeyStillRefusesToLower(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host driver runs natively only")
	}
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	entry := filepath.Join(dir, "monomorphised_float_key.fern")
	if err := os.WriteFile(entry, []byte(monomorphisedFloatKeySrc), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	if _, code := runFixtureInterp(t, entry, ""); code == 0 {
		t.Errorf("native ran a program with a monomorphised f64 map key; it reported E045 when this " +
			"test was written, so either the instantiation re-check regressed or the rule moved")
	}

	route, _ := exec.Command(driver, entry, root, "-decide").Output()
	if got := strings.TrimSpace(string(route)); got != "refused" {
		t.Errorf("-decide = %q, want \"refused\": lowering this f64 key without the checker "+
			"puts the key's VALUE through the string column as an address", got)
	}
}
