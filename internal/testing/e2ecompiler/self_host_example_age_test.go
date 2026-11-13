package e2ecompiler

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostExampleAgeUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/age/age_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// ageReport is the last line of each age error. The port does not print it:
// it points to the issue tracker of age.
const ageReport = "age: report unexpected or unhelpful errors at https://filippo.io/age/report\n"

// ageTool runs one implementation of age in a directory.
type ageTool struct {
	name string
	cmd  func(args ...string) *exec.Cmd
	// The program path, which the port shows in place of "age" in a hint.
	bin string
}

func (a ageTool) run(t *testing.T, dir string, stdin []byte, args ...string) ([]byte, string, int) {
	t.Helper()
	cmd := a.cmd(args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", a.name, &stderr)
	}
	msg := strings.ReplaceAll(stderr.String(), ageReport, "")
	if a.bin != "" {
		msg = strings.ReplaceAll(msg, a.bin, "age")
	}
	return stdout.Bytes(), msg, cmd.ProcessState.ExitCode()
}

// ageOracle is /usr/bin/age 1.1.1. A missing oracle fails the test.
func ageOracle(t *testing.T) (ageTool, string) {
	t.Helper()
	age, err := exec.LookPath("age")
	if err != nil {
		t.Fatal("age is required: it is the oracle of the age example (Debian package age 1.1.1)")
	}
	keygen, err := exec.LookPath("age-keygen")
	if err != nil {
		t.Fatal("age-keygen is required: it makes the keys of the age example tests")
	}
	return ageTool{name: "age", bin: age, cmd: func(args ...string) *exec.Cmd { return exec.Command(age, args...) }}, keygen
}

// ageKey writes a new identity file with age-keygen and gives its recipient.
func ageKey(t *testing.T, keygen, path string) string {
	t.Helper()
	if out, err := exec.Command(keygen, "-o", path).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v\n%s", err, out)
	}
	out, err := exec.Command(keygen, "-y", path).Output()
	if err != nil {
		t.Fatalf("age-keygen -y: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func ageWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ageRandom(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func ageExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// agePort builds the port for target and gives a tool that runs it.
func agePort(t *testing.T, cli *selfHostCLI, target string) ageTool {
	t.Helper()
	src, err := filepath.Abs("../../../examples/age/age.fern")
	if err != nil {
		t.Fatal(err)
	}
	if target == "x86-64-linux" {
		bin := cli.x86Binary(t, src)
		return ageTool{name: "fern age", bin: bin, cmd: func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }}
	}
	_, qemu := arm64Tooling(t)
	bin := cli.arm64Binary(t, src)
	return ageTool{name: "fern age", bin: bin, cmd: func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }}
}

var ageSizes = []int{0, 1, 65535, 65536, 65537, 1<<20 + 3}

// TestSelfHostExampleAgeCLI checks the port against age 1.1.1 on x86-64 and
// arm64: each tool decrypts what the other encrypts, each refuses the same
// tampered files with the same message, and the port writes no output file
// for a file that does not decrypt.
func TestSelfHostExampleAgeCLI(t *testing.T) {
	oracle, keygen := ageOracle(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := agePort(t, cli, target)
			dir := t.TempDir()
			r1 := ageKey(t, keygen, filepath.Join(dir, "k1"))
			r2 := ageKey(t, keygen, filepath.Join(dir, "k2"))
			t.Run("from age", func(t *testing.T) { ageFromOracle(t, oracle, port, dir, r1) })
			t.Run("to age", func(t *testing.T) { ageToOracle(t, oracle, port, dir, r1, r2) })
			t.Run("tampered", func(t *testing.T) { ageTampered(t, oracle, port, dir, r1) })
			t.Run("malformed", func(t *testing.T) { ageMalformed(t, oracle, port, dir, r1) })
			t.Run("messages", func(t *testing.T) { ageMessages(t, oracle, port, dir, r1) })
		})
	}
}

func ageFromOracle(t *testing.T, oracle, port ageTool, dir, r1 string) {
	for _, size := range ageSizes {
		plain := ageRandom(t, size)
		for _, armor := range [][]string{nil, {"-a"}} {
			name := fmt.Sprintf("%d%v", size, armor)
			sealed, stderr, code := oracle.run(t, dir, plain, append([]string{"-r", r1}, armor...)...)
			if code != 0 {
				t.Fatalf("age %s: exit %d: %s", name, code, stderr)
			}
			ageWrite(t, filepath.Join(dir, "in.age"), sealed)
			out := filepath.Join(dir, "out")
			os.Remove(out)
			if _, stderr, code := port.run(t, dir, nil, "-d", "-i", "k1", "-o", "out", "in.age"); code != 0 || stderr != "" {
				t.Fatalf("fern age -d of %s: exit %d: %s", name, code, stderr)
			}
			got, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(got, plain) {
				t.Errorf("fern age -d -o of %s differs from the plaintext", name)
			}
			if got, stderr, code := port.run(t, dir, sealed, "--decrypt", "--identity", "k1"); code != 0 || !bytes.Equal(got, plain) {
				t.Errorf("fern age -d of %s on stdin: exit %d: %s", name, code, stderr)
			}
		}
	}
}

func ageToOracle(t *testing.T, oracle, port ageTool, dir, r1, r2 string) {
	ageWrite(t, filepath.Join(dir, "recipients"), []byte("# two keys\n"+r2+"\n\n"))
	for _, size := range ageSizes {
		plain := ageRandom(t, size)
		ageWrite(t, filepath.Join(dir, "plain"), plain)
		for _, armor := range [][]string{nil, {"-a"}} {
			name := fmt.Sprintf("%d%v", size, armor)
			args := append([]string{"-r", r1, "-R", "recipients", "-o", "out.age"}, armor...)
			if _, stderr, code := port.run(t, dir, nil, append(args, "plain")...); code != 0 || stderr != "" {
				t.Fatalf("fern age %s: exit %d: %s", name, code, stderr)
			}
			sealed, err := os.ReadFile(filepath.Join(dir, "out.age"))
			if err != nil {
				t.Fatal(err)
			}
			if len(armor) > 0 && !bytes.HasPrefix(sealed, []byte("-----BEGIN AGE ENCRYPTED FILE-----\n")) {
				t.Errorf("fern age -a %s: no armor header", name)
			}
			for _, key := range []string{"k1", "k2"} {
				got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", key)
				if code != 0 || !bytes.Equal(got, plain) {
					t.Errorf("age -d -i %s of fern age %s: exit %d: %s", key, name, code, stderr)
				}
			}
		}
	}
	// -e -i encrypts to the recipient of an identity file.
	sealed, stderr, code := port.run(t, dir, []byte("to myself\n"), "-e", "-i", "k1")
	if code != 0 {
		t.Fatalf("fern age -e -i: exit %d: %s", code, stderr)
	}
	if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", "k1"); code != 0 || string(got) != "to myself\n" {
		t.Errorf("age -d of fern age -e -i: exit %d: %s", code, stderr)
	}
}

// ageHeaderEnd is the index just after the MAC line.
func ageHeaderEnd(t *testing.T, sealed []byte) int {
	t.Helper()
	i := bytes.Index(sealed, []byte("\n--- "))
	if i < 0 {
		t.Fatal("no MAC line")
	}
	return i + 1 + bytes.IndexByte(sealed[i+1:], '\n') + 1
}

func ageFlip(b []byte, at int, mask byte) []byte {
	out := append([]byte{}, b...)
	out[at] ^= mask
	return out
}

func ageTampered(t *testing.T, oracle, port ageTool, dir, r1 string) {
	plain := ageRandom(t, 150000)
	fromAge, stderr, code := oracle.run(t, dir, plain, "-r", r1)
	if code != 0 {
		t.Fatalf("age: exit %d: %s", code, stderr)
	}
	fromPort, stderr, code := port.run(t, dir, plain, "-r", r1)
	if code != 0 {
		t.Fatalf("fern age: exit %d: %s", code, stderr)
	}
	for _, src := range []struct {
		name   string
		sealed []byte
	}{{"age", fromAge}, {"fern age", fromPort}} {
		end := ageHeaderEnd(t, src.sealed)
		mac := bytes.Index(src.sealed, []byte("\n--- ")) + 10
		body := bytes.IndexByte(src.sealed[len("age-encryption.org/v1\n"):], '\n') + len("age-encryption.org/v1\n") + 5
		n := len(src.sealed)
		for _, tc := range []struct {
			name string
			data []byte
		}{
			{"a bit in the header", ageFlip(src.sealed, body, 1)},
			{"a bit in the MAC", ageFlip(src.sealed, mac, 1)},
			{"a bit in a chunk", ageFlip(src.sealed, end+16+70000, 0x80)},
			{"a bit in the last tag", ageFlip(src.sealed, n-1, 1)},
			{"a truncated last chunk", src.sealed[:n-1]},
			{"no last chunk", src.sealed[:end+16+2*65552]},
			{"an extra chunk", append(append([]byte{}, src.sealed...), src.sealed[end+16:end+16+65552]...)},
			{"a trailing byte", append(append([]byte{}, src.sealed...), 0)},
		} {
			name := src.name + ": " + tc.name
			ageWrite(t, filepath.Join(dir, "bad.age"), tc.data)
			theirs, want, wantCode := oracle.run(t, dir, nil, "-d", "-i", "k1", "bad.age")
			ours, got, gotCode := port.run(t, dir, nil, "-d", "-i", "k1", "bad.age")
			if wantCode != 1 || gotCode != 1 || got != want {
				t.Errorf("%s: age exit %d %q, fern age exit %d %q", name, wantCode, want, gotCode, got)
			}
			if !bytes.Equal(ours, theirs) {
				t.Errorf("%s: fern age wrote %d bytes to the standard output, age %d", name, len(ours), len(theirs))
			}
			os.Remove(filepath.Join(dir, "absent"))
			if _, _, code := port.run(t, dir, nil, "-d", "-i", "k1", "-o", "absent", "bad.age"); code != 1 || ageExists(filepath.Join(dir, "absent")) {
				t.Errorf("%s: fern age -o must fail and write no file", name)
			}
			ageWrite(t, filepath.Join(dir, "kept"), []byte("old\n"))
			port.run(t, dir, nil, "-d", "-i", "k1", "-o", "kept", "bad.age")
			if old, _ := os.ReadFile(filepath.Join(dir, "kept")); string(old) != "old\n" {
				t.Errorf("%s: fern age -o changed a file that was there", name)
			}
			if entries, _ := filepath.Glob(filepath.Join(dir, ".age-*")); len(entries) != 0 {
				t.Errorf("%s: fern age left %v", name, entries)
			}
		}
	}
}

// ageMalformed builds malformed files from one that age encrypts and checks
// that the port gives the message and the exit code of age for each one.
func ageMalformed(t *testing.T, oracle, port ageTool, dir, r1 string) {
	small, _, _ := oracle.run(t, dir, []byte("hello"), "-r", r1)
	big, _, _ := oracle.run(t, dir, ageRandom(t, 153600), "-r", r1)
	empty, _, _ := oracle.run(t, dir, nil, "-r", r1)
	armored, _, _ := oracle.run(t, dir, []byte("hello"), "-a", "-r", r1)
	mac := bytes.Index(small, []byte("\n--- ")) + 1
	end := ageHeaderEnd(t, small)
	lines := strings.Split(string(small[:mac]), "\n")
	stanza, body := lines[1], lines[2]
	at := strings.Split(string(armored), "\n")
	b64 := func(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }
	edit := func(old, new string) []byte { return []byte(strings.Replace(string(small), old, new, 1)) }
	armorEdit := func(old, new string) []byte { return []byte(strings.Replace(string(armored), old, new, 1)) }
	cases := []struct {
		name string
		data []byte
	}{
		{"empty file", nil},
		{"intro v2", edit("age-encryption.org/v1", "age-encryption.org/v2")},
		{"intro only", []byte("age-encryption.org/v1")},
		{"intro CRLF", edit("\n", "\r\n")},
		{"intro UTF-16", append([]byte{0xff, 0xfe}, []byte("a\x00g\x00e\x00-\x00e\x00n\x00c\x00r\x00y\x00p\x00t\x00")...)},
		{"no MAC", small[:mac]},
		{"MAC without newline", small[:end-1]},
		{"MAC of 31 bytes", edit(string(small[mac:end]), "--- "+b64(bytes.Repeat([]byte{1}, 31))+"\n")},
		{"two MAC arguments", edit(string(small[mac:end]), "--- abc def\n")},
		{"MAC prefix", edit("\n---", "\n---x")},
		{"MAC CRLF", edit(string(small[mac:end]), strings.TrimSuffix(string(small[mac:end]), "\n")+"\r\n")},
		{"MAC short", edit(string(small[mac:end]), string(small[mac:mac+4])+string(small[mac+5:end]))},
		{"header cut", small[:mac-1]},
		{"stanza without type", edit("-> X25519 ", "->  ")},
		{"stanza arrow only", edit(stanza+"\n", "->\n")},
		{"stanza control byte", edit("X25519", "X\x7f519")},
		{"stanza prefix", edit("-> X25519", "->X X25519")},
		{"unknown line", edit("-> X25519", "-- X25519")},
		{"body padding", edit(body, body+"=")},
		{"body CRLF", edit(body+"\n", body+"\r\n")},
		{"body bad byte", edit(body, body[:5]+"!"+body[6:])},
		{"body trailing bits", edit(body, body[:len(body)-1]+map[bool]string{true: "e", false: "d"}[body[len(body)-1] == 'd'])},
		{"body of 68 columns", edit(body, strings.Repeat("A", 68))},
		{"body with no short line", edit(body, strings.Repeat("A", 64))},
		{"X25519 two arguments", edit("-> X25519 ", "-> X25519 abc ")},
		{"X25519 padded share", edit(stanza, "-> X25519 abc=")},
		{"X25519 short share", edit(stanza, "-> X25519 AAAA")},
		{"X25519 low order share", edit(stanza, "-> X25519 "+b64(make([]byte, 32)))},
		{"X25519 long body", edit(body, b64(make([]byte, 33)))},
		{"other stanza first", edit("-> X25519", "-> foo bar\n\n-> X25519")},
		{"no nonce", small[:end]},
		{"short nonce", small[:end+10]},
		{"nonce only", small[:end+16]},
		{"empty payload", empty},
		{"empty last chunk", append(append([]byte{}, big[:len(big)-22544]...), big[len(big)-16:]...)},
		{"armor", armored},
		{"armor CRLF", []byte(strings.ReplaceAll(string(armored), "\n", "\r\n"))},
		{"armor trailing space", append(append([]byte{}, armored...), "\n \n\t\n"...)},
		{"armor trailing data", append(append([]byte{}, armored...), "x\n"...)},
		{"armor 2000 spaces", append(append([]byte{}, armored...), strings.Repeat(" ", 2000)...)},
		{"armor long line", armorEdit(at[1]+"\n", at[1]+"AAAA\n")},
		{"armor short line", armorEdit(at[1]+"\n", at[1][:60]+"\n"+at[1][60:]+"\n")},
		{"armor no footer", []byte(strings.Join(at[:len(at)-2], "\n") + "\n")},
		{"armor bad footer", armorEdit("-----END AGE ENCRYPTED FILE-----", "-----END AGE ENCRYPTED FILE----")},
		{"armor bad byte", armorEdit(at[2], "!"+at[2][1:])},
		{"armor no padding", armorEdit("==\n", "\n")},
		{"armor first line", armorEdit("-----BEGIN AGE ENCRYPTED FILE-----", "-----BEGIN AGE ENCRYPTED FILE----- ")},
		{"armor footer without newline", bytes.TrimSuffix(armored, []byte("\n"))},
		{"armor empty", []byte("-----BEGIN AGE ENCRYPTED FILE-----\n-----END AGE ENCRYPTED FILE-----\n")},
		{"armor header only", []byte("-----BEGIN AGE ENCRYPTED FILE-----\n")},
		{"armor header without newline", []byte("-----BEGIN AGE ENCRYPTED FILE-----")},
	}
	for _, tc := range cases {
		ageWrite(t, filepath.Join(dir, "m.age"), tc.data)
		theirs, want, wantCode := oracle.run(t, dir, nil, "-d", "-i", "k1", "m.age")
		ours, got, gotCode := port.run(t, dir, nil, "-d", "-i", "k1", "m.age")
		if gotCode != wantCode || got != want || !bytes.Equal(ours, theirs) {
			t.Errorf("%s: age exit %d %q; fern age exit %d %q", tc.name, wantCode, want, gotCode, got)
		}
	}
	// The port follows the age specification where age 1.1.1 does not (the
	// C2SP CCTV vectors armor_cr_*, armor_double_crlf, armor_empty_last_line,
	// armor_whitespace_outside and scrypt_and_x25519).
	// 40 bytes of plaintext give 240 bytes of age file: five full lines.
	full, _, _ := oracle.run(t, dir, ageRandom(t, 40), "-a", "-r", r1)
	if fl := strings.Split(string(full), "\n"); len(fl[len(fl)-3]) != 64 {
		t.Fatalf("the armor of 40 bytes does not end with a full line: %q", full)
	}
	for _, tc := range []struct {
		name, msg string
		data      []byte
		code      int
	}{
		{"armor after white space", "", append([]byte("\n \t\n"), armored...), 0},
		{"armor CR in a line", "age: error: failed to read header: parsing age header: failed to read intro: invalid armor: illegal base64 data at input byte 10\n", armorEdit(at[1], at[1][:10]+"\r"+at[1][11:]), 1},
		{"armor empty last line", "age: error: invalid armor: empty line before the closing line\n", []byte(strings.Replace(string(full), "\n-----END", "\n\n-----END", 1)), 1},
		{"scrypt with another stanza", "age: error: an scrypt recipient must be the only one\n", edit("-> X25519", "-> scrypt c2FsdA 10\n\n-> X25519"), 1},
	} {
		ageWrite(t, filepath.Join(dir, "m.age"), tc.data)
		_, got, code := port.run(t, dir, nil, "-d", "-i", "k1", "m.age")
		if code != tc.code || got != tc.msg {
			t.Errorf("%s: fern age exit %d %q; want exit %d %q", tc.name, code, got, tc.code, tc.msg)
		}
	}
}

func ageMessages(t *testing.T, oracle, port ageTool, dir, r1 string) {
	sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-r", r1)
	ageWrite(t, filepath.Join(dir, "c.age"), sealed)
	ageWrite(t, filepath.Join(dir, "p"), []byte("hello"))
	k1, err := os.ReadFile(filepath.Join(dir, "k1"))
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	for _, line := range strings.Split(string(k1), "\n") {
		if strings.HasPrefix(line, "AGE-SECRET-KEY-1") {
			key = line
		}
	}
	change := func(s string, i int, c byte) string { b := []byte(s); b[i] = c; return string(b) }
	for name, content := range map[string]string{
		"id-empty":    "",
		"id-comments": "# a\n\n",
		"id-lower":    strings.ToLower(key) + "\n",
		"id-mixed":    key[:20] + strings.ToLower(key[20:]) + "\n",
		"id-checksum": change(key, len(key)-1, map[bool]byte{true: 'Q', false: 'P'}[key[len(key)-1] == 'P']) + "\n",
		"id-char":     key[:len(key)-3] + "B" + key[len(key)-2:] + "\n",
		"id-space":    " " + key + "\n",
		"id-trailing": key + " \n",
		"id-garbage":  "hello\n",
		"id-second":   "garbage\n" + key + "\n",
		"id-crlf":     "# c\r\n" + key + "\r\n",
		"rf-bad":      "# c\n" + r1 + "\nbogus\n",
		"rf-empty":    "# c\n",
		"rf-long":     strings.Repeat("x", 9000) + "\n",
	} {
		ageWrite(t, filepath.Join(dir, name), []byte(content))
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	upper := strings.ToUpper(r1)
	cases := [][]string{
		{"-d", "-i", "k2", "c.age"},
		{"-d", "c.age"},
		{"-d", "-i", "nosuch", "c.age"},
		{"-d", "-i", "k1", "nosuch"},
		{"-r", r1, "nosuch"},
		{"-r", "bogus", "p"},
		{"-r", "", "p"},
		{"-r", "age1", "p"},
		{"-r", "age1qqq", "p"},
		{"-r", upper, "p"},
		{"-r", r1[:10] + upper[10:], "p"},
		{"-r", change(r1, len(r1)-1, map[bool]byte{true: 'q', false: 'p'}[r1[len(r1)-1] == 'p']), "p"},
		{"-r", r1[:len(r1)-3] + "b" + r1[len(r1)-2:], "p"},
		{"-r", r1 + " ", "p"},
		{"-r", "age1foo1qqqqqqqqqqqqq", "p"},
		{"-r", key, "p"},
		{"-e", "-d"},
		{"-d", "-a", "-i", "k1", "c.age"},
		{"-d", "-p"},
		{"-d", "-r", r1, "c.age"},
		{"-d", "-R", "x"},
		{"-i", "k1", "p"},
		{"-e"},
		{"-p", "-r", r1},
		{"p", "q"},
		{"-r", r1, "p", "-a"},
		{"-r", r1, "p", "x y"},
		{"-R", "rf-bad", "p"},
		{"-R", "rf-empty", "p"},
		{"-R", "rf-long", "p"},
		{"-R", "nosuch", "p"},
		{"-R", "sub", "p"},
		{"-r", r1, "-o", "nodir/x", "p"},
		{"-r", r1, "-o", "sub", "p"},
		{"-d", "-i", "sub", "c.age"},
		{"-d", "-i", "k1", "sub"},
		{"-d", "-i", "k1", "--", "-x"},
		{"-d", "-i", "-", "-"},
		{"-x"},
		{"--bogus"},
		{"-o"},
		{"-a=x", "-r", r1},
		{"---x"},
	}
	for name := range map[string]bool{"id-empty": true, "id-comments": true, "id-lower": true, "id-mixed": true, "id-checksum": true, "id-char": true, "id-space": true, "id-trailing": true, "id-garbage": true, "id-second": true} {
		cases = append(cases, []string{"-d", "-i", name, "c.age"})
	}
	for _, args := range cases {
		_, want, wantCode := oracle.run(t, dir, []byte("hello"), args...)
		_, got, gotCode := port.run(t, dir, []byte("hello"), args...)
		if wantCode == 2 {
			// A flag error: Go's message, then the usage, which differs.
			want, got = strings.SplitN(want, "\n", 2)[0], strings.SplitN(got, "\n", 2)[0]
		}
		if gotCode != wantCode || got != want {
			t.Errorf("age %s: age exit %d %q; fern age exit %d %q", strings.Join(args, " "), wantCode, want, gotCode, got)
		}
	}
	for _, args := range [][]string{{"-d", "-i", "id-crlf", "c.age"}, {"-d", "-i", "k2", "-i", "k1", "c.age"}, {"-d", "-i", "-", "c.age"}} {
		stdin := []byte("hello")
		if args[2] == "-" {
			stdin = k1
		}
		if got, stderr, code := port.run(t, dir, stdin, args...); code != 0 || string(got) != "hello" {
			t.Errorf("fern age %s: exit %d: %s", strings.Join(args, " "), code, stderr)
		}
	}
	if _, stderr, code := port.run(t, dir, nil, "-h"); code != 0 || !strings.HasPrefix(stderr, "Usage:") {
		t.Errorf("fern age -h: exit %d: %s", code, stderr)
	}
	if _, stderr, code := port.run(t, dir, nil); code != 1 || !strings.HasPrefix(stderr, "Usage:") {
		t.Errorf("fern age with no arguments: exit %d: %s", code, stderr)
	}
}

// ageVector is one file of the C2SP CCTV age suite (examples/age/testdata/cctv).
type ageVector struct {
	name, expect, payload string
	identities            []string
	file                  []byte
}

func ageVectors(t *testing.T) []ageVector {
	t.Helper()
	dir, err := filepath.Abs("../../../examples/age/testdata/cctv")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []ageVector
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		head, body, ok := bytes.Cut(raw, []byte("\n\n"))
		if !ok {
			t.Fatalf("%s: no blank line after the header", e.Name())
		}
		v := ageVector{name: e.Name(), file: body}
		compressed := false
		for _, line := range strings.Split(string(head), "\n") {
			key, value, _ := strings.Cut(line, ": ")
			switch key {
			case "expect":
				v.expect = value
			case "payload":
				v.payload = value
			case "identity":
				v.identities = append(v.identities, value)
			case "compressed":
				compressed = value == "zlib"
			}
		}
		if compressed {
			zr, err := zlib.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("%s: %v", v.name, err)
			}
			if v.file, err = io.ReadAll(zr); err != nil {
				t.Fatalf("%s: %v", v.name, err)
			}
		}
		out = append(out, v)
	}
	if len(out) != 147 {
		t.Fatalf("found %d CCTV vectors, want the 147 of commit b88c4b20", len(out))
	}
	return out
}

// ageOutcome is the CCTV result that a run of the port shows.
func ageOutcome(code int, msg string) string {
	if code == 0 {
		return "success"
	}
	m := strings.TrimPrefix(strings.TrimSuffix(msg, "\n"), "age: error: ")
	switch {
	case strings.Contains(m, "invalid armor: "):
		return "armor failure"
	case m == "no identity matched any of the recipients":
		return "no match"
	case m == "bad header MAC":
		return "HMAC failure"
	}
	for _, p := range []string{"failed to read header: ", "invalid header intro", "failed to read nonce: ", "invalid X25519 recipient", "failed to parse X25519 recipient: ", "an scrypt recipient must be the only one"} {
		if strings.HasPrefix(m, p) {
			return "header failure"
		}
	}
	for _, p := range []string{"failed to decrypt and authenticate payload chunk", "unexpected EOF", "trailing data after end of encrypted file", "last chunk is empty"} {
		if strings.HasPrefix(m, p) {
			return "payload failure"
		}
	}
	return "other: " + m
}

// ageCCTVPending holds the vectors that need a recipient type after slice 1
// of #15587: scrypt (a passphrase) and the ML-KEM hybrid. The test fails when
// one of them starts to give its expected result.
var ageCCTVPending = map[string]bool{
	"armor_hybrid":                            true,
	"armor_scrypt":                            true,
	"hybrid":                                  true,
	"hybrid_and_x25519":                       true,
	"hybrid_bad_tag":                          true,
	"hybrid_currupted_enc_mlkem":              true,
	"hybrid_currupted_enc_x25519":             true,
	"hybrid_extra_argument":                   true,
	"hybrid_grease":                           true,
	"hybrid_identity":                         true,
	"hybrid_long_file_key":                    true,
	"hybrid_long_share":                       true,
	"hybrid_low_order":                        true,
	"hybrid_multiple_recipients":              true,
	"hybrid_no_match":                         true,
	"hybrid_not_canonical_body":               true,
	"hybrid_not_canonical_enc":                true,
	"hybrid_short_share":                      true,
	"hybrid_uppercase":                        true,
	"hybrid_x25519_arg":                       true,
	"scrypt":                                  true,
	"scrypt_bad_tag":                          true,
	"scrypt_extra_argument":                   true,
	"scrypt_long_file_key":                    true,
	"scrypt_no_match":                         true,
	"scrypt_not_canonical_salt":               true,
	"scrypt_salt_long":                        true,
	"scrypt_salt_missing":                     true,
	"scrypt_salt_short":                       true,
	"scrypt_work_factor_23":                   true,
	"scrypt_work_factor_hex":                  true,
	"scrypt_work_factor_leading_garbage":      true,
	"scrypt_work_factor_leading_plus":         true,
	"scrypt_work_factor_leading_zero_decimal": true,
	"scrypt_work_factor_leading_zero_octal":   true,
	"scrypt_work_factor_missing":              true,
	"scrypt_work_factor_negative":             true,
	"scrypt_work_factor_overflow":             true,
	"scrypt_work_factor_trailing_garbage":     true,
	"scrypt_work_factor_wrong":                true,
	"scrypt_work_factor_zero":                 true,
}

// ageCCTVNotArmored holds the armor failures that do not start with the armor
// header, after white space. The port reads them as plain files, as age does,
// so the failure is in the header.
var ageCCTVNotArmored = map[string]bool{
	"armor_garbage_leading":  true,
	"armor_lowercase":        true,
	"armor_whitespace_begin": true,
	"armor_wrong_type":       true,
}

// TestSelfHostExampleAgeCCTV decrypts each vector of the C2SP CCTV age suite
// with the port on x86-64 and arm64. Each one gives its expected result, and
// the plaintext that the port writes before a failure has the vector's hash.
func TestSelfHostExampleAgeCCTV(t *testing.T) {
	vectors := ageVectors(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := agePort(t, cli, target)
			dir := t.TempDir()
			for _, v := range vectors {
				ageCCTVOne(t, port, dir, v)
			}
		})
	}
}

func ageCCTVOne(t *testing.T, port ageTool, dir string, v ageVector) {
	var ids []byte
	for _, id := range v.identities {
		ids = append(ids, id+"\n"...)
	}
	ageWrite(t, filepath.Join(dir, "id"), ids)
	ageWrite(t, filepath.Join(dir, "v.age"), v.file)
	args := []string{"-d"}
	if len(v.identities) > 0 {
		args = append(args, "-i", "id")
	}
	out, msg, code := port.run(t, dir, nil, append(args, "v.age")...)
	got := ageOutcome(code, msg)
	want := v.expect
	if ageCCTVNotArmored[v.name] {
		want = "header failure"
	}
	if ageCCTVPending[v.name] {
		if got == want {
			t.Errorf("%s: now gives %q: remove it from ageCCTVPending", v.name, got)
		}
		return
	}
	if got != want {
		t.Errorf("%s: got %s, want %s (%q)", v.name, got, want, msg)
		return
	}
	if v.payload != "" && fmt.Sprintf("%x", sha256.Sum256(out)) != v.payload {
		t.Errorf("%s: the %d bytes of plaintext do not have the payload hash", v.name, len(out))
	}
	os.Remove(filepath.Join(dir, "out"))
	_, _, code = port.run(t, dir, nil, append(args, "-o", "out", "v.age")...)
	if written := ageExists(filepath.Join(dir, "out")); written != (code == 0) {
		t.Errorf("%s: exit %d, and an output file is there: %v", v.name, code, written)
	}
}

// The identity of the CCTV x25519 vector and its recipient, and a second key.
const (
	ageGateKey       = "AGE-SECRET-KEY-1EGTZVFFV20835NWYV6270LXYVK2VKNX2MMDKWYKLMGR48UAWX40Q2P2LM0"
	ageGateRecipient = "age1xmwwc06ly3ee5rytxm9mflaz2u56jjj36s0mypdrwsvlul66mv4q47ryef"
	ageGateOtherKey  = "AGE-SECRET-KEY-1NE2ZYPW0VVQG3QYWMQ3MLNQMYQ5XXEEKLXDU32EAVYWL6A2AZ0RQHMW3MS"
)

// ageCtGate runs the port under memcheck, which reports a branch, an index or
// a write that depends on a secret byte: the data part of a key line, the
// file key or an ephemeral scalar. bin runs on this host.
func ageCtGate(t *testing.T, valgrind, bin string) {
	t.Helper()
	dir := t.TempDir()
	plain := bytes.Repeat([]byte("0123456789abcdef"), 9000)
	ageWrite(t, filepath.Join(dir, "id"), []byte("# the gate key\n"+ageGateKey+"\n"))
	ageWrite(t, filepath.Join(dir, "other"), []byte(ageGateOtherKey+"\n"))
	ageWrite(t, filepath.Join(dir, "broken"), []byte(ageGateKey[:len(ageGateKey)-1]+"Q\n"))
	ageWrite(t, filepath.Join(dir, "plain"), plain)
	run := func(memcheck bool, args ...string) (int, string) {
		cmd := exec.Command(bin, args...)
		if memcheck {
			cmd = exec.Command(valgrind, append([]string{"--error-exitcode=3", "-q", bin}, args...)...)
		}
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode(), stderr.String()
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"encrypt", []string{"-r", ageGateRecipient, "-o", "c.age", "plain"}, 0},
		{"encrypt armored to an identity", []string{"-a", "-e", "-i", "id", "-o", "a.age", "plain"}, 0},
		{"decrypt", []string{"-d", "-i", "other", "-i", "id", "-o", "p1", "c.age"}, 0},
		{"decrypt armored", []string{"-d", "-i", "id", "-o", "p2", "a.age"}, 0},
		{"no identity matches", []string{"-d", "-i", "other", "c.age"}, 1},
		{"a malformed key", []string{"-d", "-i", "broken", "c.age"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, stderr := run(false, tc.args...); code != tc.code {
				t.Fatalf("native run exited %d, want %d\n%s", code, tc.code, stderr)
			}
			code, stderr := run(true, tc.args...)
			if code != tc.code || strings.Contains(stderr, "==") {
				t.Fatalf("memcheck exited %d, want %d with no report\n%s", code, tc.code, stderr)
			}
		})
	}
	for _, p := range []string{"p1", "p2"} {
		if got, err := os.ReadFile(filepath.Join(dir, p)); err != nil || !bytes.Equal(got, plain) {
			t.Errorf("%s differs from the plaintext", p)
		}
	}
}

// ageGateBinary compiles the port with a self-host CLI for target.
func ageGateBinary(t *testing.T, runner []string, cli, stdlib, target string) string {
	t.Helper()
	src, err := filepath.Abs("../../../examples/age/age.fern")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "age")
	if msg, err := runX86_64Bin(runner, cli, "-target", target, "-o", bin, src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target %s age: %v\n%s", target, err, msg)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
