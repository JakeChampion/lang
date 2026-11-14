package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/tools/tty"
)

// agePrompt is a prompt of age that asks for a passphrase.
var agePrompt = regexp.MustCompile(`passphrase[^\r\n]*: `)

// ageQuiet waits until the terminal echoes nothing, so that a passphrase
// never shows in the transcript.
func ageQuiet(master *os.File) {
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		words, err := tty.Termios(int(master.Fd()))
		if err != nil || words[3]&syscall.ECHO == 0 {
			return
		}
	}
}

// runTTY runs the tool with a new pseudo-terminal as its controlling
// terminal. It types line k after prompt k that asks for a passphrase. The
// result has the transcript of the terminal.
func (a ageTool) runTTY(t *testing.T, dir string, stdin []byte, lines []string, args ...string) ([]byte, string, string, int) {
	t.Helper()
	typed := make([]string, len(lines))
	for i, l := range lines {
		typed[i] = l + "\n"
	}
	return a.runTTYPrompt(t, dir, stdin, agePrompt, typed, args...)
}

// runTTYPrompt is runTTY with another prompt. It types each input after the
// prompt as it is, with no new line after it.
func (a ageTool) runTTYPrompt(t *testing.T, dir string, stdin []byte, prompt *regexp.Regexp, lines []string, args ...string) ([]byte, string, string, int) {
	t.Helper()
	return a.runPTY(t, dir, ptyStreams{stdin: stdin}, prompt, lines, args...)
}

// ptyStreams says which standard streams are the terminal. With stdinTTY,
// the test types pre at the terminal before the first prompt.
type ptyStreams struct {
	stdin               []byte
	stdinTTY, stdoutTTY bool
	pre                 string
}

func (a ageTool) runPTY(t *testing.T, dir string, streams ptyStreams, prompt *regexp.Regexp, lines []string, args ...string) ([]byte, string, string, int) {
	t.Helper()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("open a pseudo-terminal: %v", err)
	}
	defer master.Close()
	cmd := a.cmd(args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(streams.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if streams.stdinTTY {
		cmd.Stdin = slave
	}
	if streams.stdoutTTY {
		cmd.Stdout = slave
	}
	cmd.ExtraFiles = []*os.File{slave}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s did not start: %v", a.name, err)
	}
	slave.Close()
	if streams.pre != "" {
		go master.Write([]byte(streams.pre))
	}
	var screen bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		typed := 0
		for {
			n, err := master.Read(buf)
			screen.Write(buf[:n])
			for typed < len(lines) && len(prompt.FindAll(screen.Bytes(), -1)) > typed {
				ageQuiet(master)
				master.Write([]byte(lines[typed]))
				typed++
			}
			if err != nil {
				return
			}
		}
	}()
	timer := time.AfterFunc(5*time.Minute, func() { cmd.Process.Kill() })
	_ = cmd.Wait()
	timer.Stop()
	<-done
	msg := strings.ReplaceAll(stderr.String(), ageReport, "")
	if a.bin != "" {
		msg = strings.ReplaceAll(msg, a.bin, "age")
	}
	return stdout.Bytes(), msg, screen.String(), cmd.ProcessState.ExitCode()
}

// ageTerminalInput checks an armored file that the user types at the
// terminal: both tools read it up to the end of the armor before they ask
// for the passphrase, and hold the plaintext for a terminal until the end.
func ageTerminalInput(t *testing.T, oracle, port ageTool, dir string) {
	plain := []byte("typed at the terminal\n")
	ageWrite(t, filepath.Join(dir, "typed"), plain)
	if _, stderr, _, code := oracle.runTTY(t, dir, nil, []string{"pass", "pass"}, "-p", "-a", "-o", "typed.age", "typed"); code != 0 {
		t.Fatalf("age -p -a: exit %d %s", code, stderr)
	}
	armored := ageRead(t, filepath.Join(dir, "typed.age"))
	for _, c := range []struct {
		streams ptyStreams
		lines   []string
		args    []string
	}{
		{ptyStreams{stdinTTY: true, pre: armored}, []string{"pass\n"}, []string{"-d"}},
		{ptyStreams{stdinTTY: true, stdoutTTY: true, pre: armored}, []string{"pass\n"}, []string{"-d"}},
		{ptyStreams{stdinTTY: true, stdoutTTY: true, pre: armored}, []string{"pass\n"}, []string{"-d", "-o", "-"}},
		{ptyStreams{stdinTTY: true, pre: armored}, []string{"wrong\n"}, []string{"-d"}},
	} {
		wantOut, want, wantScreen, wantCode := oracle.runPTY(t, dir, c.streams, agePrompt, c.lines, c.args...)
		gotOut, got, screen, gotCode := port.runPTY(t, dir, c.streams, agePrompt, c.lines, c.args...)
		if gotCode != wantCode || got != want || screen != wantScreen || !bytes.Equal(gotOut, wantOut) {
			t.Errorf("age %v with %v: age exit %d %q %q %q; fern age exit %d %q %q %q", c.args, c.lines, wantCode, want, wantScreen, wantOut, gotCode, got, screen, gotOut)
		}
	}
}

// runDetached runs the tool in a new session with no controlling terminal.
func (a ageTool) runDetached(t *testing.T, dir string, stdin []byte, args ...string) (string, int) {
	t.Helper()
	cmd := a.cmd(args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", a.name, &stderr)
	}
	msg := strings.ReplaceAll(stderr.String(), ageReport, "")
	if a.bin != "" {
		msg = strings.ReplaceAll(msg, a.bin, "age")
	}
	return msg, cmd.ProcessState.ExitCode()
}

// ageCCTVFile is the age file of a CCTV vector, with no header lines.
func ageCCTVFile(t *testing.T, name string) []byte {
	t.Helper()
	for _, v := range ageVectors(t) {
		if v.name == name {
			return v.file
		}
	}
	t.Fatalf("no CCTV vector %s", name)
	return nil
}

var ageAutoPassphrase = regexp.MustCompile(`age: using autogenerated passphrase "([a-z]+(?:-[a-z]+){9})"\r\n`)

// agePassphrase checks scrypt recipients and passphrases against age 1.3.2:
// each tool decrypts what the other encrypts, and both give the same
// messages, the same exit codes and the same terminal transcript.
func agePassphrase(t *testing.T, oracle, port ageTool, dir, keygen, r1 string) {
	plain := ageRandom(t, 70000)
	ageWrite(t, filepath.Join(dir, "plain"), plain)
	twice := []string{"pass phrase", "pass phrase"}
	for _, armor := range [][]string{nil, {"-a"}} {
		args := append([]string{"-p", "-o", "p.age"}, append(armor, "plain")...)
		_, want, wantScreen, wantCode := oracle.runTTY(t, dir, nil, twice, args...)
		_, got, screen, code := port.runTTY(t, dir, nil, twice, args...)
		if code != 0 || code != wantCode || got != want || screen != wantScreen {
			t.Fatalf("fern age %v: exit %d %q %q; age exit %d %q %q", args, code, got, screen, wantCode, want, wantScreen)
		}
		out, stderr, _, code := oracle.runTTY(t, dir, nil, twice[:1], "-d", "p.age")
		if code != 0 || !bytes.Equal(out, plain) {
			t.Errorf("age -d of fern age %v: exit %d %s", args, code, stderr)
		}
	}
	if _, stderr, _, code := oracle.runTTY(t, dir, nil, twice, "-p", "-a", "-o", "a.age", "plain"); code != 0 {
		t.Fatalf("age -p: exit %d %s", code, stderr)
	}
	os.Remove(filepath.Join(dir, "out"))
	if _, stderr, _, code := port.runTTY(t, dir, nil, twice[:1], "-d", "-o", "out", "a.age"); code != 0 {
		t.Errorf("fern age -d of an age -p file: exit %d %s", code, stderr)
	} else if got, _ := os.ReadFile(filepath.Join(dir, "out")); !bytes.Equal(got, plain) {
		t.Errorf("fern age -d of an age -p file differs from the plaintext")
	}

	// An empty passphrase gives 10 words, which the terminal shows.
	_, stderr, screen, code := port.runTTY(t, dir, nil, []string{""}, "-p", "-o", "auto.age", "plain")
	m := ageAutoPassphrase.FindStringSubmatch(screen)
	if code != 0 || m == nil {
		t.Fatalf("fern age -p with an empty passphrase: exit %d %s %q", code, stderr, screen)
	}
	if out, stderr, _, code := oracle.runTTY(t, dir, nil, m[1:], "-d", "auto.age"); code != 0 || !bytes.Equal(out, plain) {
		t.Errorf("age -d with the passphrase that fern age made: exit %d %s", code, stderr)
	}

	// An identity file that age encrypts with a passphrase.
	for _, args := range [][]string{{"-p", "-o", "k1.age", "k1"}, {"-p", "-a", "-o", "k1.asc", "k1"}} {
		if _, stderr, _, code := oracle.runTTY(t, dir, nil, []string{"id pass", "id pass"}, args...); code != 0 {
			t.Fatalf("age %v: exit %d %s", args, code, stderr)
		}
	}
	sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-r", r1)
	ageWrite(t, filepath.Join(dir, "c.age"), sealed)
	other := ageKey(t, keygen, filepath.Join(dir, "k3"))
	sealed, _, _ = oracle.run(t, dir, []byte("hello"), "-r", other)
	ageWrite(t, filepath.Join(dir, "c3.age"), sealed)
	ageWrite(t, filepath.Join(dir, "s.age"), ageCCTVFile(t, "scrypt"))
	cases := []struct {
		lines []string
		args  []string
	}{
		{[]string{"a", "b"}, []string{"-p", "-o", "x.age", "plain"}},
		{[]string{"password"}, []string{"-d", "s.age"}},
		{[]string{"wrong"}, []string{"-d", "s.age"}},
		{[]string{""}, []string{"-d", "s.age"}},
		{[]string{"\x04wrong"}, []string{"-d", "s.age"}},
		{[]string{"\x04a", "a"}, []string{"-p", "-o", "x.age", "plain"}},
		{nil, []string{"-d", "-i", "k1", "s.age"}},
		{nil, []string{"-d", "c.age"}},
		{[]string{"id pass"}, []string{"-d", "-i", "k1.age", "c.age"}},
		{[]string{"id pass"}, []string{"-d", "-i", "k1.asc", "c.age"}},
		{[]string{"wrong"}, []string{"-d", "-i", "k1.age", "c.age"}},
		{[]string{"id pass"}, []string{"-d", "-i", "k1.age", "c3.age"}},
		{[]string{"id pass"}, []string{"-d", "-i", "k1.age", "-i", "k3", "c3.age"}},
		{[]string{"password"}, []string{"-d", "-i", "s.age", "c.age"}},
		{[]string{"id pass"}, []string{"-e", "-i", "k1.age", "-o", "x.age", "plain"}},
	}
	for _, tc := range cases {
		wantOut, want, wantScreen, wantCode := oracle.runTTY(t, dir, nil, tc.lines, tc.args...)
		gotOut, got, screen, code := port.runTTY(t, dir, nil, tc.lines, tc.args...)
		if code != wantCode || got != want || screen != wantScreen || !bytes.Equal(gotOut, wantOut) {
			t.Errorf("%v %q: age exit %d %q %q; fern age exit %d %q %q", tc.args, tc.lines, wantCode, want, wantScreen, code, got, screen)
		}
	}
	// With no terminal, age says where it looked for one.
	for _, args := range [][]string{{"-p", "-o", "x.age", "plain"}, {"-d", "s.age"}, {"-d", "-i", "k1.age", "c.age"}, {"-e", "-i", "k1.age", "plain"}} {
		want, wantCode := oracle.runDetached(t, dir, nil, args...)
		got, code := port.runDetached(t, dir, nil, args...)
		if code != wantCode || got != want {
			t.Errorf("%v with no terminal: age exit %d %q; fern age exit %d %q", args, wantCode, want, code, got)
		}
	}
}
