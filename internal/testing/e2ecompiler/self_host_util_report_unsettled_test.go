package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// util.report_unsettled names the pass, its round count and, when given, the
// function, on one stderr line.
func TestSelfHostUtilReportUnsettled(t *testing.T) {
	src := "import \"./util\";\n\n" +
		"function main(): i32 {\n" +
		"  util.report_unsettled(\"semthread.thread\", 8, \"app__main\");\n" +
		"  util.report_unsettled(\"ir.strength_fixpoint\", 8, \"\");\n" +
		"  return 0;\n}\n"
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "util.fern")
	if err := os.WriteFile(filepath.Join(dir, "unsettled.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "unsettled.fern", "unsettled")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("unsettled: %v\n%s", err, out)
	}
	want := "fern: internal: semthread.thread stopped after 8 rounds before it settled in app__main (#14583)\n" +
		"fern: internal: ir.strength_fixpoint stopped after 8 rounds before it settled (#14583)\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
