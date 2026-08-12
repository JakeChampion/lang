package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmOptionPayload pins the wasm backend's Option/Result payload
// extractors (examples/self_host/wasm_ir.fern's parse_option_payload /
// parse_result_err_payload — SH-021, docs/SELF-HOST-AUDIT.md T2). Both now decode
// an Option[T] / Result[T, E] spelling via the structured TypeRef
// (parser.parse_type_ref) instead of the magic-byte `Option[` / `Result[` prefix +
// top-level-comma depth scan.
//
// On every valid input the new decode agrees with the former scan byte-for-byte;
// the golden's two array rows (Option[i32][], Result[i32, u32][]) show the
// deliberate correction — the old scan returned garbage there (prefix matched,
// trailing `]` passed its last-char test), the TypeRef decode peels the trailing
// `[]` into array_depth and returns "" (an array is not an Option/Result). The
// three x86 self-compile fixpoints confirm no such array type reaches these during
// bootstrap, so the correction leaves the self-compile byte-identical.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostWasmOptionPayload(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("wasm_option_payload_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "astwalk.fern", "ir.fern", "parser.fern",
		"asmcore.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_option_payload_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "wasm_option_payload_run.fern", "wasm_option_payload_run")

	const want = "Option[i32] opt=i32 err=<empty>\n" +
		"Option[u32] opt=u32 err=<empty>\n" +
		"Option[string] opt=string err=<empty>\n" +
		"Option[Foo] opt=Foo err=<empty>\n" +
		"Option[Map[a, b]] opt=Map[a, b] err=<empty>\n" +
		"Option[(string, string)] opt=(string, string) err=<empty>\n" +
		"Option[i32][] opt=<empty> err=<empty>\n" +
		"Option[] opt=<empty> err=<empty>\n" +
		"Option opt=<empty> err=<empty>\n" +
		"Result[i32, u32] opt=i32 err=u32\n" +
		"Result[string, IoError] opt=string err=IoError\n" +
		"Result[i32] opt=i32 err=<empty>\n" +
		"Result[Map[a, b], i32] opt=Map[a, b] err=i32\n" +
		"Result[i32, u32][] opt=<empty> err=<empty>\n" +
		"Result[i32, u32, x] opt=i32 err=u32\n" +
		"Result opt=<empty> err=<empty>\n" +
		"i32 opt=<empty> err=<empty>\n" +
		"string opt=<empty> err=<empty>\n" +
		"Foo opt=<empty> err=<empty>\n" +
		"u8[] opt=<empty> err=<empty>\n" +
		"<empty> opt=<empty> err=<empty>\n" +
		"Map[string, i32] opt=<empty> err=<empty>\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm_option_payload_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("wasm option/result payload decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm_option_payload_run exit code = %d, want 0", code)
	}
}
