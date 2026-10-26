package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type hostFs struct {
	blockSize, blocks, blocksFree, blocksAvail int64
	files, filesFree                           int64
	nameMax, pathMax                           int64
	fsType, fsid, fragSize                     int64
}

func selfHostStatfsSource(dir string, h hostFs, missing string) string {
	slackBlocks := h.blocks/64 + 4096
	slackFiles := h.files/64 + 4096
	return fmt.Sprintf(`function main(): i32 {
    match (statfs(%[1]q)) {
        Ok(fs) => {
            if (fs.block_size != (%[2]d as i64)) { return 1; }
            if (fs.name_max != (%[3]d as i64)) { return 2; }
            if (fs.path_max != (%[4]d as i64)) { return 3; }
            if (fs.blocks != (%[5]d as i64)) { return 4; }
            if (fs.files != (%[6]d as i64)) { return 5; }
            if (fs.blocks_free < (%[7]d as i64)) { return 6; }
            if (fs.blocks_free > (%[8]d as i64)) { return 7; }
            if (fs.blocks_avail < (%[9]d as i64)) { return 8; }
            if (fs.blocks_avail > (%[10]d as i64)) { return 9; }
            if (fs.files_free < (%[11]d as i64)) { return 10; }
            if (fs.files_free > (%[12]d as i64)) { return 11; }
            if (fs.blocks_avail > fs.blocks_free) { return 12; }
            if (fs.blocks_free > fs.blocks) { return 13; }
            if (fs.files_free > fs.files) { return 14; }
            if (fs.fs_type != %[14]di64) { return 17; }
            if (fs.fsid != %[15]di64) { return 18; }
            if (fs.frag_size != %[16]di64) { return 19; }
        },
        Err(_) => { return 15; }
    }
    // A path that does not resolve is an Err, not a zero-filled record.
    match (statfs(%[13]q)) {
        Ok(_) => { return 16; },
        Err(_) => {}
    }
    return 0;
}
`, dir, h.blockSize, h.nameMax, h.pathMax, h.blocks, h.files,
		h.blocksFree-slackBlocks, h.blocksFree+slackBlocks,
		h.blocksAvail-slackBlocks, h.blocksAvail+slackBlocks,
		h.filesFree-slackFiles, h.filesFree+slackFiles,
		missing, h.fsType, h.fsid, h.fragSize)
}

func statfsProbeSource(t *testing.T, dir string) string {
	t.Helper()
	return selfHostStatfsSource(dir, selfHostFsFacts(t, dir), filepath.Join(dir, "no-such-dir", "x"))
}

func TestSelfHostStatfsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("statfs test runs only natively (measures host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(statfsProbeSource(t, work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fn___fern_statfs")) {
		t.Fatal("statfs did not reach the IR runtime path (no __fn___fern_statfs in the asm)")
	}
	progBin := buildBin(t, gcc, dir, "statfs_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("statfs program exited %d, want 0 — the code names the field (see selfHostStatfsSource)", code)
	}
}

func TestSelfHostStatfsArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("statfs test runs only natively (measures host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(statfsProbeSource(t, work)), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the statfs program")
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_statfs")) {
		t.Fatal("statfs did not reach the arm64 IR runtime path (no `bl __fn___fern_statfs` in the asm)")
	}
	progBin := buildBin(t, arm64gcc, dir, "statfs_prog", string(asm))
	run := runArm64Bin(qemu, progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 statfs program exited %d, want 0 — the code names the field (see selfHostStatfsSource)", code)
	}
}

func TestSelfHostStatfsWasmIRRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := `function main(): i32 {
    match (statfs(".")) { Ok(fs) => { return fs.name_max as i32; }, Err(_) => { return 1; } }
}
`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("the wasm driver accepted statfs (exit 0); it has no volume to measure\n--- stdout ---\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "required by `statfs`; targets providing it") {
		t.Fatalf("the wasm refusal does not name the builtin:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("the wasm driver emitted a module as well as refusing:\n%s", stdout.String())
	}
}

func TestSelfHostStatfsLayoutMatchesTheHost(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skipf("no C compiler, so the host's <asm-generic/statfs.h> cannot be consulted: %v", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.c")
	const probe = `#include <asm-generic/statfs.h>
#include <stddef.h>
#include <stdio.h>
int main(void){
  printf("size %zu word %zu bsize %zu blocks %zu bfree %zu bavail %zu files %zu ffree %zu namelen %zu type %zu fsid %zu frsize %zu\n",
    sizeof(struct statfs), sizeof(((struct statfs*)0)->f_bsize),
    offsetof(struct statfs, f_bsize), offsetof(struct statfs, f_blocks),
    offsetof(struct statfs, f_bfree), offsetof(struct statfs, f_bavail),
    offsetof(struct statfs, f_files), offsetof(struct statfs, f_ffree),
    offsetof(struct statfs, f_namelen), offsetof(struct statfs, f_type),
    offsetof(struct statfs, f_fsid), offsetof(struct statfs, f_frsize));
  return 0;
}
`
	if err := os.WriteFile(src, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(cc, "-o", bin, src).CombinedOutput(); err != nil {
		t.Skipf("the kernel uapi headers are not installed, so there is nothing to measure against: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run the statfs layout probe: %v", err)
	}
	fields := strings.Fields(string(out))
	host := map[string]int64{}
	for i := 0; i+1 < len(fields); i += 2 {
		n, err := strconv.ParseInt(fields[i+1], 10, 64)
		if err != nil {
			t.Fatalf("parse the probe's answer %q: %v", out, err)
		}
		host[fields[i]] = n
	}

	got := parseStatfsOffsets(t, "statfsoff_linux")
	for _, c := range []struct{ field, member string }{
		{"block_size", "bsize"},
		{"blocks", "blocks"},
		{"blocks_free", "bfree"},
		{"blocks_avail", "bavail"},
		{"files", "files"},
		{"files_free", "ffree"},
		{"name_max", "namelen"},
		{"fs_type", "type"},
		{"fsid", "fsid"},
		{"frag_size", "frsize"},
	} {
		want, ok := host[c.member]
		if !ok {
			t.Fatalf("the probe reported no offset for f_%s", c.member)
		}
		if got[c.field] != want {
			t.Errorf("asmcore reads FsStat.%s from offset %d; the host's struct statfs puts f_%s at %d",
				c.field, got[c.field], c.member, want)
		}
	}
	if n := host["size"]; n > int64(statfsBufSize(t)) {
		t.Errorf("the host's struct statfs is %d bytes; the Linux helper hands the kernel a %d-byte buffer",
			n, statfsBufSize(t))
	}
	if w := host["word"]; w != 8 {
		t.Fatalf("the host's __statfs_word is %d bytes, not 8 — the projection assumes a 64-bit word", w)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			source, fields := runtimeStatfsFields(t, target)
			for field, offset := range got {
				if field == "fsid" {
					checkStatfsLoads(t, fields, field, 32, offset, offset+4)
				} else {
					checkStatfsLoads(t, fields, field, 64, offset)
				}
			}
			// fs_type is the table's zero-offset fallthrough.
			checkStatfsLoads(t, fields, "fs_type", 64, host["type"])
			buf := regexp.MustCompile(`let buf: usize = __raw_scratch\((\d+)\);`).FindStringSubmatch(source)
			if buf == nil {
				t.Fatal("runtime statfs helper has no scratch buffer declaration")
			}
			n, err := strconv.ParseInt(buf[1], 10, 64)
			if err != nil || n < host["size"] {
				t.Errorf("runtime buffer is %q bytes; the host struct needs %d", buf[1], host["size"])
			}
		})
	}
	t.Run("arm64-darwin", func(t *testing.T) {
		_, fields := runtimeStatfsFields(t, "arm64-darwin")
		for field, offset := range map[string]int64{"block_size": 0, "frag_size": 0, "fs_type": 60} {
			checkStatfsLoads(t, fields, field, 32, offset)
			if !strings.Contains(fields[field], "4294967295") {
				t.Errorf("Darwin %s must zero-extend its unsigned 32-bit field", field)
			}
		}
	})
}

// Inspect the target runtime sources used by the generated bundle, rather than
// the removed asmcore string-construction helpers.
func runtimeStatfsFields(t *testing.T, target string) (string, map[string]string) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compiler", "runtime", target, "__fern_statfs.fern")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`(?s)return Ok\(FsStat\s*\{(.*?)\}\);`).FindSubmatch(src)
	if literal == nil {
		t.Fatalf("%s has no FsStat projection", path)
	}
	fields := map[string]string{}
	for _, row := range strings.Split(string(literal[1]), ",") {
		name, value, ok := strings.Cut(row, ":")
		if !ok {
			t.Fatalf("invalid FsStat field %q", row)
		}
		name = strings.TrimSpace(name)
		if _, exists := fields[name]; exists {
			t.Fatalf("duplicate FsStat field %s", name)
		}
		fields[name] = strings.TrimSpace(value)
	}
	return string(src), fields
}

func checkStatfsLoads(t *testing.T, fields map[string]string, field string, bits int, offsets ...int64) {
	t.Helper()
	value, ok := fields[field]
	if !ok {
		t.Errorf("runtime statfs projection has no field %s", field)
		return
	}
	loads := regexp.MustCompile(`__load_i(32|64)\(\s*buf\s*\+\s*(\d+)(?:\s*\+\s*(\d+))?\s*\)`).FindAllStringSubmatch(value, -1)
	if len(loads) != len(offsets) {
		t.Errorf("%s has %d buffer loads, want %d: %s", field, len(loads), len(offsets), value)
		return
	}
	for i, load := range loads {
		n, err := strconv.ParseInt(load[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if load[3] != "" {
			add, err := strconv.ParseInt(load[3], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			n += add
		}
		if load[1] != strconv.Itoa(bits) || n != offsets[i] {
			t.Errorf("%s load %d is %s at offset %d, want i%d at offset %d", field, i, load[1], n, bits, offsets[i])
		}
	}
}

func parseStatfsOffsets(t *testing.T, fn string) map[string]int64 {
	t.Helper()
	body := fernFunctionBody(t, fn)
	row := regexp.MustCompile(`if \(name == "([a-z_]+)"\)\s*\{\s*return "(\d+)";\s*\}`)
	out := map[string]int64{}
	for _, m := range row.FindAllStringSubmatch(body, -1) {
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			t.Fatalf("parse %s row %q: %v", fn, m[0], err)
		}
		out[m[1]] = n
	}
	if len(out) == 0 {
		t.Fatalf("no offset rows found in %s — has the table been reshaped?", fn)
	}
	return out
}

func fernFunctionBody(t *testing.T, fn string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "asmcore.fern"))
	if err != nil {
		t.Fatal(err)
	}
	head := regexp.MustCompile(`(?m)^(?:pub )?function ` + regexp.QuoteMeta(fn) + `\(`)
	loc := head.FindIndex(src)
	if loc == nil {
		t.Fatalf("asmcore.fern declares no function %s", fn)
	}
	rest := string(src[loc[0]:])
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end]
	}
	t.Fatalf("%s in asmcore.fern has no closing brace", fn)
	return ""
}

func splitDarwinFork(body string) (darwin, rest string) {
	i := strings.Index(body, `arm64-darwin`)
	if i < 0 {
		return "", body
	}
	j := strings.Index(body[i:], "}")
	if j < 0 {
		return body[i:], body[:i]
	}
	return body[i : i+j], body[:i] + body[i+j:]
}

func statfsBufSize(t *testing.T) int {
	t.Helper()
	body := fernFunctionBody(t, "statfs_bufsize_n")
	_, linuxArm := splitDarwinFork(body)
	m := regexp.MustCompile(`return (\d+);`).FindStringSubmatch(linuxArm)
	if m == nil {
		t.Fatalf("statfs_bufsize_n has no Linux fallthrough:\n%s", body)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return n
}
