package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// wordsDriver emits one program for a native target twice, once as text alone
// and once handing the assembler encoded instructions where the emitter has a
// record form, and assembles both. The two must produce the same code and
// data: a record is only ever the encoding its text would have assembled to.
const wordsDriver = `import "./parser"; import "./lexer"; import "./checker"; import "./asm_arm64_ir"; import "./arm64_native";
import "./asm_ir"; import "./x86_native"; import "./util";
import "./modloader"; import "./flatten"; import "./treeshake"; import "./semlower";
function first_diff(a: i32[], b: i32[]): i32 {
    let n: i32 = a.len();
    if (b.len() < n) { n = b.len(); }
    let i: i32 = 0;
    while (i < n) {
        if (a[i] != b[i]) { return i; }
        i = i + 1;
    }
    if (a.len() != b.len()) { return n; }
    return 0 - 1;
}
function report(records: i32, code: i32, unknown: string[], code_diff: i32, data_diff: i32): void {
    print("records " + util.i32_to_string(records));
    print("code " + util.i32_to_string(code));
    print("unknown " + util.i32_to_string(unknown.len()));
    for u in unknown { print("  " + u); }
    print("code_diff " + util.i32_to_string(code_diff));
    print("data_diff " + util.i32_to_string(data_diff));
}
function main(): i32 {
    let av = args();
    let target: string = av[1];
    let src: string = "";
    match (read_file(av[2])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    let entry = parser.parse_module(lexer.tokenize(src));
    let (loaded, missing) = modloader.load_imports(modloader.no_overlay(), av[2], entry);
    if (modloader.report_unresolved(missing, "words")) {
        return 2;
    }
    let merged = flatten.bundle(entry, loaded, "");
    let shaken = treeshake.treeshake(checker.annotate_module(merged));
    let d = semlower.driven_annotated(shaken, target);
    let recs: usize = buf_new(4096);
    if (target == "arm64-linux") {
        let text: string = asm_arm64_ir.emit_module_or_error_sub(d.full, false, d.sub, 0 as usize);
        let mixed: string = asm_arm64_ir.emit_module_or_error_sub(d.full, false, d.sub, recs);
        let words: u8[] = buf_take_bytes(recs);
        let a = arm64_native.arm64_gas_program(text);
        let b = arm64_native.arm64_gas_program_words(mixed, words);
        report(words.len() / 8, a.asm.code.len() / 4, b.unknown, first_diff(a.asm.code, b.asm.code), first_diff(a.data, b.data));
        return 0;
    }
    let xtext: string = asm_ir.emit_module_or_error_sub(d.full, d.sub, 0 as usize);
    let xmixed: string = asm_ir.emit_module_or_error_sub(d.full, d.sub, recs);
    let xwords: u8[] = buf_take_bytes(recs);
    let xa = x86_native.x86_gas_assemble(xtext);
    let xb = x86_native.x86_gas_assemble_words(xmixed, xwords);
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < xwords.len()) {
        n = n + 1;
        i = i + 1 + xwords[i] as i32;
    }
    report(n, xa.code.len(), xb.unknown, first_diff(xa.code, xb.code), first_diff(xa.rodata, xb.rodata));
    return 0;
}
`

func TestSelfHostWordsMatchText(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "words.fern"), []byte(wordsDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "words.fern", "words")
	program := semsourceProgram(t, semsourceRCProgram)
	// insns is how many instructions the program assembles to, as the code
	// length: each record is one instruction, so `insns(code) / min` is a floor
	// under the share of instructions that travel as records.
	for _, tc := range []struct {
		target string
		insns  func(code int) int
		min    int
	}{
		// About two fifths of this program's arm64 instructions are words.
		{"arm64-linux", func(code int) int { return code }, 4},
		// x86 instructions average about four bytes; a third or so are records.
		{"x86-64-linux", func(code int) int { return code / 4 }, 5},
	} {
		t.Run(tc.target, func(t *testing.T) {
			cmd := runX86_64Bin(runner, driver, tc.target, program)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, stderr.String())
			}
			field := func(name string) int {
				m := regexp.MustCompile(`(?m)^` + name + ` (-?\d+)`).FindSubmatch(out)
				if m == nil {
					t.Fatalf("no %q line in driver output:\n%s", name, out)
				}
				n, _ := strconv.Atoi(string(m[1]))
				return n
			}
			if field("unknown") != 0 {
				t.Fatalf("an assembly refused:\n%s", out)
			}
			// Under the floor means the emitter stopped handing records over.
			if records, insns := field("records"), tc.insns(field("code")); records*tc.min < insns {
				t.Errorf("only %d records against about %d instructions:\n%s", records, insns, out)
			}
			if at := field("code_diff"); at >= 0 {
				t.Errorf("code differs from the text path's at byte %d:\n%s", at, out)
			}
			if at := field("data_diff"); at >= 0 {
				t.Errorf("data differs from the text path's at byte %d:\n%s", at, out)
			}
			t.Logf("%s", out)
		})
	}
}
