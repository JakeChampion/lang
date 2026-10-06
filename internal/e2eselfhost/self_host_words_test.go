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
// and once handing the assembler records where the emitter has a form for
// them (encoded instructions, label ids, branches by id), and assembles both.
// The two must produce the same code, data and unwind image: a record is only
// ever what its text would have assembled to, and the `.cfi_*` lines between
// records land where they landed between the text lines.
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
function first_diff_u8(a: u8[], b: u8[]): i32 {
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
// marks counts the record lines of the mixed text: the lines a marker byte
// (1 to 5) starts, each one record the assembler takes in place of text.
function marks(text: string): i32 {
    let n: i32 = 0;
    let at_start: boolean = true;
    let i: i32 = 0;
    while (i < text.len()) {
        if (at_start && text[i] >= 1 && text[i] <= 5) { n = n + 1; }
        at_start = text[i] == b'\n';
        i = i + 1;
    }
    return n;
}
// text_insns counts the instruction lines the mixed text still hands the
// assembler as text: an indented line that is not a record, a label or a
// directive.
function text_insns(text: string): i32 {
    let n: i32 = 0;
    let at: i32 = 0;
    while (at < text.len()) {
        let end: i32 = at;
        while (end < text.len() && text[end] != b'\n') { end = end + 1; }
        if (end - at > 4 && text[at] == b' ' && text[at + 4] != b'.') { n = n + 1; }
        at = end + 1;
    }
    return n;
}
function report(records: i32, text: i32, code: i32, unknown: string[], code_diff: i32, data_diff: i32, eh_diff: i32): void {
    print("records " + util.i32_to_string(records));
    print("text " + util.i32_to_string(text));
    print("code " + util.i32_to_string(code));
    print("unknown " + util.i32_to_string(unknown.len()));
    for u in unknown { print("  " + u); }
    print("code_diff " + util.i32_to_string(code_diff));
    print("data_diff " + util.i32_to_string(data_diff));
    print("eh_diff " + util.i32_to_string(eh_diff));
}
function main(): i32 {
    let av = args();
    let target: string = av[1];
    let src: string = "";
    match (read_file(av[2])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    let entry = parser.parse_module(lexer.tokenize(src));
    let g: modloader.Graph = modloader.load_graph(modloader.no_overlay(), av[2], entry, "");
    entry = g.entry;
    let loaded = g.loaded;
    let missing = g.missing;
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
        let eh: i32 = first_diff(arm64_native.arm64_eh_frame(a, 0 as i64, 0 as i64), arm64_native.arm64_eh_frame(b, 0 as i64, 0 as i64));
        report(marks(mixed), text_insns(mixed), a.asm.text.len() / 4, b.unknown, first_diff_u8(a.asm.text, b.asm.text), first_diff(a.data, b.data), eh);
        return 0;
    }
    let xtext: string = asm_ir.emit_module_or_error_sub(d.full, d.sub, 0 as usize);
    let xmixed: string = asm_ir.emit_module_or_error_sub(d.full, d.sub, recs);
    let xwords: u8[] = buf_take_bytes(recs);
    let xa = x86_native.x86_gas_assemble(xtext);
    let xb = x86_native.x86_gas_assemble_words(xmixed, xwords);
    let xeh: i32 = first_diff(x86_native.x86_eh_frame(xa, 0 as i64, 0 as i64), x86_native.x86_eh_frame(xb, 0 as i64, 0 as i64));
    report(marks(xmixed), text_insns(xmixed), xa.text.len(), xb.unknown, first_diff_u8(xa.text, xb.text), first_diff(xa.rodata, xb.rodata), xeh);
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
	// share is the floor, in percent, under the share of the program's
	// instructions that travel as records rather than text: under it, an
	// emitter site stopped handing records over.
	for _, tc := range []struct {
		target string
		share  int
	}{
		// arm64 sits near 86% on this program and x86 near 80%, most of
		// the text being the hand-written runtime; x86 was 46% before its
		// frames, moves and calls became records.
		{"arm64-linux", 70},
		{"x86-64-linux", 70},
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
			if records, text := field("records"), field("text"); records*100 < (records+text)*tc.share {
				t.Errorf("only %d records beside %d text instructions, under %d%%:\n%s", records, text, tc.share, out)
			}
			if at := field("code_diff"); at >= 0 {
				t.Errorf("code differs from the text path's at byte %d:\n%s", at, out)
			}
			if at := field("data_diff"); at >= 0 {
				t.Errorf("data differs from the text path's at byte %d:\n%s", at, out)
			}
			if at := field("eh_diff"); at >= 0 {
				t.Errorf(".eh_frame differs from the text path's at byte %d:\n%s", at, out)
			}
			t.Logf("%s", out)
		})
	}
}
