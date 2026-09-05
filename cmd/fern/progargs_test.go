package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The parsing rules behind `args()`: one leading `--` is the driver/program
// separator and disappears, later `--` are data, and a flag written past FILE
// is refused instead of silently becoming a program argument (#8465).
func TestProgramArgs(t *testing.T) {
	cases := []struct {
		name    string
		rest    []string
		want    []string
		wantErr string
	}{
		{name: "empty", rest: nil, want: nil},
		{name: "plain", rest: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "separator_consumed", rest: []string{"--", "a", "b"}, want: []string{"a", "b"}},
		{name: "separator_only", rest: []string{"--"}, want: []string{}},
		{name: "flags_after_separator", rest: []string{"--", "-x", "--verbose"}, want: []string{"-x", "--verbose"}},
		{name: "literal_separator_is_data", rest: []string{"--", "--", "x"}, want: []string{"--", "x"}},
		{name: "later_separator_is_data", rest: []string{"a", "--", "b"}, want: []string{"a", "--", "b"}},
		{name: "bare_dash_is_data", rest: []string{"-", "a"}, want: []string{"-", "a"}},
		{name: "short_flag_refused", rest: []string{"-o", "out"}, wantErr: "-o comes after the source file"},
		{name: "long_flag_refused", rest: []string{"--verbose"}, wantErr: "--verbose comes after the source file"},
		{name: "driver_flag_refused", rest: []string{"-interp"}, wantErr: "-interp comes after the source file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := programArgs(tc.rest)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("programArgs(%q) = %q, nil; want an error naming the flag", tc.rest, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), "--") {
					t.Errorf("error %q does not point at the `--` separator", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("programArgs(%q): %v", tc.rest, err)
			}
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Errorf("programArgs(%q) = %q, want %q", tc.rest, got, tc.want)
			}
		})
	}
}

// A flag after FILE must not look like a successful run: `fern FILE -o out`
// used to print assembly to stdout and exit 0, producing no file and no
// complaint (#8465).
func TestFlagAfterFileExitsNonZero(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog.bin")

	for _, tc := range []struct {
		name string
		argv []string
		flag string
	}{
		{name: "o_after_file", argv: []string{src, "-o", out}, flag: "-o"},
		{name: "interp_after_file", argv: []string{src, "-interp"}, flag: "-interp"},
		{name: "interp_mode_flag_after_file", argv: []string{"-interp", src, "-o", out}, flag: "-o"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.argv...)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			code := cmd.ProcessState.ExitCode()
			if code == 0 {
				t.Errorf("fern %s exited 0; a misplaced flag must not look like a successful run\nstdout:\n%s", strings.Join(tc.argv, " "), firstLines(stdout.String(), 5))
			}
			if !strings.Contains(stderr.String(), tc.flag) {
				t.Errorf("stderr does not name %s:\n%s", tc.flag, stderr.String())
			}
			if _, err := os.Stat(out); err == nil {
				t.Errorf("%s was written even though the command was refused", out)
			}
		})
	}
}
