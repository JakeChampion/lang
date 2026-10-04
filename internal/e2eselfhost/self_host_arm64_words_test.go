package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// arm64WordsDriver emits one program for arm64-linux twice, once as text alone
// and once handing the assembler encoded words where the emitter has a record
// form, and assembles both. The two must produce the same code and data: a
// word is only ever the encoding its text would have assembled to.
const arm64WordsDriver = `import "./parser"; import "./lexer"; import "./checker"; import "./asm_arm64_ir"; import "./arm64_native"; import "./util";
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
function main(): i32 {
    let av = args();
    let src: string = "";
    match (read_file(av[1])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    let entry = parser.parse_module(lexer.tokenize(src));
    let (loaded, missing) = modloader.load_imports(modloader.no_overlay(), av[1], entry);
    if (modloader.report_unresolved(missing, "arm64_words")) {
        return 2;
    }
    let merged = flatten.bundle(entry, loaded, "");
    let shaken = treeshake.treeshake(checker.annotate_module(merged));
    let d = semlower.driven_annotated(shaken, "arm64-linux");
    let text: string = asm_arm64_ir.emit_module_or_error_sub(d.full, false, d.sub, 0 as usize);
    let recs: usize = buf_new(4096);
    let mixed: string = asm_arm64_ir.emit_module_or_error_sub(d.full, false, d.sub, recs);
    let words: u8[] = buf_take_bytes(recs);
    let a = arm64_native.arm64_gas_program(text);
    let b = arm64_native.arm64_gas_program_words(mixed, words);
    print("words " + util.i32_to_string(words.len() / 8));
    print("code " + util.i32_to_string(a.asm.code.len()));
    print("unknown " + util.i32_to_string(a.unknown.len()) + " " + util.i32_to_string(b.unknown.len()));
    for u in b.unknown { print("  " + u); }
    print("code_diff " + util.i32_to_string(first_diff(a.asm.code, b.asm.code)));
    print("data_diff " + util.i32_to_string(first_diff(a.data, b.data)));
    return 0;
}
`

func TestSelfHostArm64WordsMatchText(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "arm64_words.fern"), []byte(arm64WordsDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "arm64_words.fern", "arm64-words")
	program := semsourceProgram(t, semsourceRCProgram)
	cmd := runX86_64Bin(runner, driver, program)
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
	if !bytes.Contains(out, []byte("\nunknown 0 0\n")) {
		t.Fatalf("an assembly refused:\n%s", out)
	}
	// About two fifths of this program's instructions are words; under a
	// quarter means the emitter stopped handing them over.
	if words, code := field("words"), field("code"); words*4 < code/4 {
		t.Errorf("only %d of %d instructions were words:\n%s", words, code/4, out)
	}
	if at := field("code_diff"); at >= 0 {
		t.Errorf("code differs from the text path's at byte %d:\n%s", at, out)
	}
	if at := field("data_diff"); at >= 0 {
		t.Errorf("data differs from the text path's at byte %d:\n%s", at, out)
	}
	t.Logf("%s", out)
}
