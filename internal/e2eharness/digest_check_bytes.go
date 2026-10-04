package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

var DigestCheckUtilities = []string{"md5sum", "sha1sum", "sha224sum", "sha256sum", "sha384sum", "sha512sum", "b2sum", "cksum"}

// RunDigestCheckByteCases compares byte grammar with GNU, and separately pins
// D10's filename boundary errors. The latter require no raw filesystem names.
func RunDigestCheckByteCases(t *testing.T, utility, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := utility
	if runtime.GOOS == "darwin" {
		name = "g" + name
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, utility)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU checksum utility")
	}
	version, err := exec.Command(oracle, "--version").Output()
	first := strings.SplitN(string(version), "\n", 2)[0]
	if err != nil || !(strings.HasPrefix(first, utility+" (GNU coreutils) ") || strings.HasPrefix(first, utility+" (coreutils) ")) {
		t.Skip("requires GNU checksum utility")
	}
	fields := strings.Fields(first)
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized version: %s", first)
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s", first)
	dir := t.TempDir()
	for _, name := range []string{"f", "é🙂", "back\\slash", "new\nline"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := ChildEnv("LC_ALL=C")
	base := []string{}
	if utility == "cksum" {
		base = []string{"-a", "md5"}
	}
	run := func(command []string, args []string, input string) ([]byte, string, int) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		argv := append(append([]string{}, command...), args...)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(input)
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err := cmd.Run()
		if ctx.Err() != nil || cmd.ProcessState == nil {
			t.Fatalf("run: %v\n%s", err, diagnostic.String())
		}
		return out.Bytes(), diagnostic.String(), cmd.ProcessState.ExitCode()
	}
	checksum := func(tag bool, name string) string {
		args := append([]string{}, base...)
		if tag {
			args = append(args, "--tag")
		} else if utility == "cksum" {
			args = append(args, "--untagged")
		}
		out, diagnostic, code := run([]string{oracle}, append(args, name), "")
		if code != 0 || diagnostic != "" {
			t.Fatalf("generate checksum: %d %s", code, diagnostic)
		}
		return string(out)
	}
	plain, tagged := checksum(false, "f"), checksum(true, "f")
	digest := strings.Fields(plain)[0]
	tag := strings.SplitN(tagged, " (", 2)[0]
	type checkCase struct {
		name, body string
		algorithm  string
		options    []string
		invalid    bool
		noValid    bool
		stdin      bool
		detect     bool
	}
	cases := []checkCase{
		{name: "plain", body: plain},
		{name: "tagged", body: tagged},
		{name: "unicode", body: checksum(false, "é🙂")},
		{name: "escaped", body: checksum(false, "back\\slash") + checksum(true, "new\nline")},
		{name: "raw comment", body: "#\xff\xed\xa0\x80\n" + plain},
		{name: "raw digest", body: "\xff" + plain[1:] + plain, options: []string{"--warn"}},
		{name: "raw tag", body: "\xff" + tagged[1:] + tagged, options: []string{"--warn"}},
		{name: "nul tail", body: strings.TrimSuffix(plain, "\n") + "\x00\xff\n"},
		{name: "tagged nul", body: strings.TrimSuffix(tagged, "\n") + "\x00\n", options: []string{"--warn"}},
		{name: "tagged nul raw tail", body: strings.TrimSuffix(tagged, "\n") + "\x00\xff\n" + plain, options: []string{"--warn"}},
		{name: "nul prefix", body: "\x00\xff" + plain + plain, options: []string{"--warn"}},
		{name: "crlf", body: strings.ReplaceAll(plain, "\n", "\r\n")},
		{name: "no newline", body: strings.TrimSuffix(plain, "\n")},
		{name: "bad escape", body: "\\" + digest + "  bad\\\xff\n" + plain, options: []string{"--warn"}},
		{name: "line numbering", body: "#\xff\n\n\xff\n" + tagged, options: []string{"--warn"}},
		{name: "invalid name only", body: digest + "  \xff\n", invalid: true, noValid: true},
		{name: "invalid name only ignore missing", body: digest + "  \xff\n", options: []string{"--ignore-missing"}, invalid: true, noValid: true},
	}
	unicodeLine := checksum(false, "é🙂")
	nameAt := strings.Index(unicodeLine, "é")
	for _, boundary := range []int{4096, 65536} {
		for split := 1; split < len("é🙂"); split++ {
			padding := boundary - nameAt - split
			cases = append(cases, checkCase{name: fmt.Sprintf("boundary%d split%d", boundary, split), body: "#" + strings.Repeat("x", padding-2) + "\n" + unicodeLine})
		}
	}
	for i, bad := range []string{"\xff", "\xed\xa0\x80", "é\xe2\x82"} {
		lines := []string{digest + "  " + bad + "\n", tag + " (" + bad + ") = " + digest + "\n", "\\" + digest + "  " + bad + "\\n\n"}
		for form, line := range lines {
			for mode, options := range [][]string{nil, {"--quiet"}, {"--status"}, {"--warn"}, {"--strict"}, {"--ignore-missing"}} {
				cases = append(cases, checkCase{name: fmt.Sprintf("invalid%d form%d mode%d", i, form, mode), body: line + plain, options: options, invalid: true})
			}
		}
	}
	if utility == "cksum" {
		for _, algorithm := range []string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512", "blake2b", "sm3"} {
			line, diagnostic, code := run([]string{oracle}, []string{"-a", algorithm, "--tag", "f"}, "")
			if code != 0 || diagnostic != "" {
				t.Fatalf("generate %s checksum: %d %s", algorithm, code, diagnostic)
			}
			body := strings.Replace(string(line), "(f)", "(\xff)", 1) + string(line)
			cases = append(cases, checkCase{name: algorithm + " invalid name", body: body, algorithm: algorithm, invalid: true})
		}
		cases = append(cases,
			checkCase{name: "detect raw tag state", body: "SHA256 (f) = \xff\n\xff\n" + tagged, options: []string{"--warn"}, detect: true},
			checkCase{name: "detect nul tail", body: strings.TrimSuffix(tagged, "\n") + "\x00\xff\n", detect: true},
		)
	}
	// Both input channels must apply the same grammar and validation policy.
	for _, original := range append([]checkCase{}, cases...) {
		original.name += " stdin"
		original.stdin = true
		cases = append(cases, original)
	}
	normalize := func(diagnostic string) string {
		var out strings.Builder
		for _, line := range strings.Split(diagnostic, "\n") {
			if line == "" || strings.HasPrefix(line, "leakcheck:") {
				continue
			}
			_, message, ok := strings.Cut(line, ": ")
			if !ok {
				t.Fatalf("unexpected diagnostic: %q", line)
			}
			out.WriteString(message + "\n")
		}
		return out.String()
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "checks"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append([]string{}, base...)
			if tc.algorithm != "" {
				args = []string{"-a", tc.algorithm}
			}
			if tc.detect {
				args = nil
			}
			args = append(append(args, "-c"), tc.options...)
			file, input := "checks", ""
			if tc.stdin {
				file, input = "-", tc.body
			}
			args = append(args, file)
			var want []byte
			var diagnostic string
			var code int
			if tc.invalid {
				code, want = 1, []byte("f: OK\n")
				label := "checks"
				if tc.stdin {
					label = "'standard input'"
				}
				diagnostic = label + ": 1: invalid UTF-8 file name\n"
				status := len(tc.options) > 0 && tc.options[0] == "--status"
				if tc.noValid || status || len(tc.options) > 0 && tc.options[0] == "--quiet" {
					want = nil
				}
				if !status {
					diagnostic += "WARNING: 1 listed file could not be read\n"
					if tc.noValid && len(tc.options) > 0 && tc.options[0] == "--ignore-missing" {
						diagnostic += label + ": no file was verified\n"
					}
				}
			} else {
				want, diagnostic, code = run([]string{oracle}, args, input)
				diagnostic = normalize(diagnostic)
			}
			command := append(append([]string{}, runner...), bin)
			out, stderr, gotCode := run(command, args, input)
			if census != nil {
				census(t, stderr)
			}
			if gotCode != code || !bytes.Equal(out, want) || normalize(stderr) != diagnostic {
				t.Fatalf("exit=%d want=%d; output=%q want=%q; diagnostic=%q want=%q", gotCode, code, out, want, normalize(stderr), diagnostic)
			}
		})
	}
}
