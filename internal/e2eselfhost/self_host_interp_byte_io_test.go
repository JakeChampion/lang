package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func checkInterpByteIO(t *testing.T, command func(...string) *exec.Cmd, stdlib string) {
	t.Helper()
	native := buildFernCLIBin(t)
	pipelineInput := string(bytes.Repeat(e2eharness.ReaderBytesInput(), 8))
	for _, tc := range []struct {
		name, source, input, output string
	}{
		{"read-all", e2eharness.IOAllBytesProgram, string(e2eharness.IOAllBytesInput()), ""},
		{"read-stdin", e2eharness.IOStdinBytesProgram("io.read_all_stdin_bytes()", false), string(e2eharness.IOAllBytesInput()), ""},
		{"reader", e2eharness.ReaderBytesProgram, string(e2eharness.ReaderBytesInput()), ""},
		{"writer", e2eharness.WriterBytesProgram, "", string(e2eharness.WriterBytesOutput())},
		{"buffered_writer", e2eharness.BufferedWriterBytesProgram, "", string(e2eharness.BufferedWriterBytesOutput())},
		{"pipeline_empty", e2eharness.BytePipelineProgram, "", ""},
		{"pipeline_bytes", e2eharness.BytePipelineProgram, pipelineInput, pipelineInput},
		{"builder", `function main(): i32 {
    var h: usize = buf_new(1);
    var empty: u8[] = buf_take_bytes(h);
    if (empty.len() != 0) { return 1; }
    var i: i32 = 0;
    while (i < 256) { buf_push_byte(h, i); i = i + 1; }
    var data: u8[] = buf_take_bytes(h);
    buf_push_byte(h, 255);
    var next: u8[] = buf_take_bytes(h);
    buf_free(h);
    if (data.len() != 256 || next.len() != 1 || next[0] != 255) { return 2; }
    i = 0;
    while (i < 256) { if (data[i] != i as u8) { return 3; } i = i + 1; }
    return 0;
}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			got, diagnostic, code := runInterpCLI(t, command("-interp", path, stdlib), tc.input)
			want, referenceDiagnostic, referenceCode := runInterpCLI(t, exec.Command(native, "-interp", path), tc.input)
			if code != 0 || referenceCode != 0 || got != want {
				t.Fatalf("byte I/O: self-host exit=%d output length=%d stderr=%q; Go exit=%d output length=%d stderr=%q", code, len(got), diagnostic, referenceCode, len(want), referenceDiagnostic)
			}
			if got != tc.output {
				t.Fatalf("byte I/O output differs: got %d bytes, want %d", len(got), len(tc.output))
			}
		})
	}
}

func TestSelfHostInterpByteIO(t *testing.T) {
	cli := buildSelfHostCLI(t)
	checkInterpByteIO(t, func(args ...string) *exec.Cmd {
		return runX86_64Bin(cli.runner, cli.bin, args...)
	}, cli.stdlib)
}

func TestSelfHostArm64InterpByteIO(t *testing.T) {
	_, runner := arm64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinFor(t, dir, "fern.fern", "fern", e2eharness.TargetArm64Linux)
	checkInterpByteIO(t, func(args ...string) *exec.Cmd {
		return runArm64Bin(runner, cli, args...)
	}, e2eharness.SelfHostStdlibRoot(t))
}

func TestSelfHostArm64DarwinInterpByteIO(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	for _, builder := range []string{"bootstrap", "primary"} {
		t.Run(builder, func(t *testing.T) {
			dir := writeSelfHostAsmProject(t)
			copySelfHostDriver(t, dir, "fern.fern")
			var cli string
			if builder == "bootstrap" {
				cli = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
			} else {
				cli = buildSelfHostBinFor(t, dir, "fern.fern", "fern", e2eharness.TargetArm64Darwin)
			}
			checkInterpByteIO(t, func(args ...string) *exec.Cmd { return exec.Command(cli, args...) }, e2eharness.SelfHostStdlibRoot(t))
		})
	}
}
