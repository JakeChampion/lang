package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The self-host assemblers' encoding tests take GNU as as their oracle: the
// assembler whose dialect the emitters write, and the one a hand-written
// encoding is checked against. gnuAsOracle assembles a batch of cases in one
// run, each under a label of its own, and reads each case's bytes back from
// between its label and the next.

// gnuAsOracle is GNU as and the binutils that read its output, for one ISA.
type gnuAsOracle struct {
	as, objcopy, nm, ld string
	asArgs              []string
}

// gnuX86Oracle is GNU as for x86-64, or skips the test.
func gnuX86Oracle(t *testing.T) gnuAsOracle {
	t.Helper()
	return gnuOracle(t, []string{"as"}, "", []string{"--64"}, "\tretq\n")
}

// gnuArm64Oracle is GNU as for aarch64: the cross assembler, or the host's on
// an arm64 host. Skips the test when neither is present.
func gnuArm64Oracle(t *testing.T) gnuAsOracle {
	t.Helper()
	names := []string{"aarch64-linux-gnu-as"}
	prefix := "aarch64-linux-gnu-"
	if runtime.GOARCH == "arm64" {
		names = append(names, "as")
	}
	o := gnuOracle(t, names, prefix, nil, "\tret\n")
	return o
}

func gnuOracle(t *testing.T, names []string, prefix string, asArgs []string, probe string) gnuAsOracle {
	t.Helper()
	var o gnuAsOracle
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			o.as = p
			if n == "as" {
				prefix = ""
			}
			break
		}
	}
	if o.as == "" {
		t.Skipf("none of %v on PATH", names)
	}
	for _, tool := range []struct {
		dst  *string
		name string
	}{{&o.objcopy, "objcopy"}, {&o.nm, "nm"}, {&o.ld, "ld"}} {
		p, err := exec.LookPath(prefix + tool.name)
		if err != nil {
			t.Skipf("%s not on PATH", prefix+tool.name)
		}
		*tool.dst = p
	}
	o.asArgs = asArgs
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.s")
	if err := os.WriteFile(src, []byte("\t.text\n"+probe), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(o.as, append(append([]string{}, asArgs...), src, "-o", filepath.Join(dir, "probe.o"))...).CombinedOutput(); err != nil {
		t.Skipf("%s does not assemble the probe: %v\n%s", o.as, err, out)
	}
	return o
}

// gnuCase is one case's result: its bytes, or why GNU as rejected it.
type gnuCase struct {
	bytes    []byte
	rejected string
}

var gasErrLine = regexp.MustCompile(`(?m)^[^:\n]*\.s:(\d+): Error: (.*)$`)

// assemble runs every case through GNU as. A case may span lines. A rejected
// case is reported as such and the rest are assembled without it.
func (o gnuAsOracle) assemble(t *testing.T, lines []string) []gnuCase {
	t.Helper()
	out := make([]gnuCase, len(lines))
	live := map[int]bool{}
	for i := range lines {
		live[i] = true
	}
	dir := t.TempDir()
	for round := 0; ; round++ {
		if round > 2*len(lines)+1 {
			t.Fatalf("GNU as kept rejecting the batch without naming the lines")
		}
		var src strings.Builder
		src.WriteString("\t.text\n")
		lineOf := map[int]int{} // .s line → case
		n := 2
		for i := range lines {
			if !live[i] {
				continue
			}
			fmt.Fprintf(&src, "fern_case_%d:\n", i)
			n++
			for _, ln := range strings.Split(lines[i], "\n") {
				src.WriteString("\t" + ln + "\n")
				lineOf[n] = i
				n++
			}
		}
		src.WriteString("fern_case_end:\n")
		sPath, oPath, bPath := filepath.Join(dir, "cases.s"), filepath.Join(dir, "cases.o"), filepath.Join(dir, "cases.text")
		if err := os.WriteFile(sPath, []byte(src.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		msg, err := exec.Command(o.as, append(append([]string{}, o.asArgs...), sPath, "-o", oPath)...).CombinedOutput()
		if err != nil {
			found := false
			for _, m := range gasErrLine.FindAllStringSubmatch(string(msg), -1) {
				ln, _ := strconv.Atoi(m[1])
				if i, ok := lineOf[ln]; ok && live[i] {
					out[i].rejected = m[2]
					delete(live, i)
					found = true
				}
			}
			if !found {
				t.Fatalf("GNU as failed on the batch without naming a case line: %v\n%s", err, msg)
			}
			continue
		}
		if msg, err := exec.Command(o.objcopy, "-O", "binary", "--only-section=.text", oPath, bPath).CombinedOutput(); err != nil {
			t.Fatalf("objcopy: %v\n%s", err, msg)
		}
		text, err := os.ReadFile(bPath)
		if err != nil {
			t.Fatal(err)
		}
		syms, err := exec.Command(o.nm, oPath).Output()
		if err != nil {
			t.Fatalf("nm: %v", err)
		}
		at := map[string]int{}
		for _, ln := range strings.Split(string(syms), "\n") {
			f := strings.Fields(ln)
			if len(f) == 3 && strings.HasPrefix(f[2], "fern_case_") {
				v, _ := strconv.ParseInt(f[0], 16, 64)
				at[f[2]] = int(v)
			}
		}
		end := at["fern_case_end"]
		for i := len(lines) - 1; i >= 0; i-- {
			if !live[i] {
				continue
			}
			start := at[fmt.Sprintf("fern_case_%d", i)]
			out[i].bytes = append([]byte(nil), text[start:end]...)
			end = start
		}
		return out
	}
}

// text assembles src as one program and returns its .text bytes.
func (o gnuAsOracle) text(t *testing.T, src string) []byte {
	t.Helper()
	b, rejected := o.program(t, src)
	if rejected != "" {
		t.Fatalf("GNU as rejects the snippet, so it cannot be the oracle for it:\n%s", rejected)
	}
	return b
}

// program assembles src as one program and returns its .text bytes, or GNU
// as's diagnostics when it rejects src.
func (o gnuAsOracle) program(t *testing.T, src string) ([]byte, string) {
	t.Helper()
	return o.build(t, src, false, ".text")
}

// data assembles src as one program and returns its .data bytes.
func (o gnuAsOracle) data(t *testing.T, src string) []byte {
	t.Helper()
	b, rejected := o.build(t, src, false, ".data")
	if rejected != "" {
		t.Fatalf("GNU as rejects the snippet, so it cannot be the oracle for it:\n%s", rejected)
	}
	return b
}

// linkedText assembles src and links it as a static executable entered at
// _start, so calls between its functions are resolved, and returns the
// linked .text bytes.
func (o gnuAsOracle) linkedText(t *testing.T, src string) []byte {
	t.Helper()
	b, rejected := o.build(t, src, true, ".text")
	if rejected != "" {
		t.Fatalf("GNU binutils reject the program, so they cannot be the oracle for it:\n%s", rejected)
	}
	return b
}

func (o gnuAsOracle) build(t *testing.T, src string, link bool, section string) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	sPath, oPath, bPath := filepath.Join(dir, "prog.s"), filepath.Join(dir, "prog.o"), filepath.Join(dir, "prog.text")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(o.as, append(append([]string{}, o.asArgs...), sPath, "-o", oPath)...).CombinedOutput(); err != nil {
		return nil, strings.TrimSpace(string(msg))
	}
	if link {
		exe := filepath.Join(dir, "prog")
		if msg, err := exec.Command(o.ld, "-static", "-e", "_start", oPath, "-o", exe).CombinedOutput(); err != nil {
			return nil, strings.TrimSpace(string(msg))
		}
		oPath = exe
	}
	if msg, err := exec.Command(o.objcopy, "-O", "binary", "--only-section="+section, oPath, bPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatal(err)
	}
	return b, ""
}

// arm64Words splits little-endian arm64 text into instruction words.
func arm64Words(text []byte) []uint32 {
	var out []uint32
	for i := 0; i+4 <= len(text); i += 4 {
		out = append(out, binary.LittleEndian.Uint32(text[i:]))
	}
	return out
}
