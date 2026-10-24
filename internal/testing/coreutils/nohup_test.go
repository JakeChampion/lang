package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("nohup", nohupCases)
}

func nohupProbe() []string {
	return []string{"sh", "-c", "echo body; echo oops >&2"}
}

func emptyTree(t *testing.T, dir string) {}

func blockedTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "nohup.out"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func blockedWithHome(t *testing.T, dir string) {
	t.Helper()
	blockedTree(t, dir)
	if err := os.Mkdir(filepath.Join(dir, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func nohupCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	for _, c := range []struct {
		name         string
		in, out, err bool
	}{
		{"tty none", false, false, false},
		{"tty err", false, false, true},
		{"tty out", false, true, false},
		{"tty out+err", false, true, true},
		{"tty in", true, false, false},
		{"tty in+err", true, false, true},
		{"tty in+out", true, true, false},
		{"tty all three", true, true, true},
	} {
		add(invocation{
			name: c.name, args: nohupProbe(),
			ttyIn: c.in, ttyOut: c.out, ttyErr: c.err,
			seedTree: emptyTree,
		})
	}

	add(invocation{
		name:     "stdin is write-only",
		args:     []string{"sh", "-c", "cat; echo cat=$?; echo hi >&0; echo wrote=$?"},
		ttyIn:    true,
		seedTree: emptyTree,
	})

	add(invocation{
		name: "appends to an existing nohup.out", args: nohupProbe(), ttyOut: true,
		seedTree: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "nohup.out"), []byte("PRIOR\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	})
	add(invocation{
		name: "falls back to $HOME", args: nohupProbe(), ttyOut: true,
		env: []string{"HOME=home"}, seedTree: blockedWithHome,
	})
	add(invocation{
		name: "both unopenable", args: nohupProbe(), ttyOut: true,
		env: []string{"HOME=."}, seedTree: blockedTree,
	})
	add(invocation{
		name: "empty $HOME names the same file", args: nohupProbe(), ttyOut: true,
		env: []string{"HOME="}, seedTree: blockedTree,
	})
	add(invocation{
		name: "unset $HOME has no fallback", args: nohupProbe(), ttyOut: true,
		seedTree: blockedTree,
	})

	add(invocation{name: "no operand"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "unknown short", args: []string{"-z", "echo", "hi"}})
	add(invocation{name: "unknown long", args: []string{"--bogus", "echo", "hi"}})
	add(invocation{name: "help with an argument", args: []string{"--help=x"}})
	add(invocation{name: "version with an argument", args: []string{"--version=x"}})
	add(invocation{name: "the command's own --help", args: []string{"echo", "--help"}})
	add(invocation{name: "the command's own -n", args: []string{"echo", "-n", "hi"}})

	add(invocation{name: "dash as the command", args: []string{"-"}})
	add(invocation{name: "an empty operand", args: []string{""}})
	add(invocation{name: "not on PATH", args: []string{"definitely-not-a-command"}})
	add(invocation{name: "PATH is searched", args: []string{"echo", "hello"}})
	add(invocation{name: "the command's status passes through", args: []string{"sh", "-c", "exit 7"}})
	add(invocation{
		name: "no execute bit", args: []string{"./x"},
		seedTree: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "x"), []byte("#!/bin/sh\necho ran\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	})
	add(invocation{
		name: "a directory", args: []string{"./dir"},
		seedTree: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	})

	return cases
}

func TestNohup(t *testing.T) {
	requireParity(t, "nohup", nohupCases(t))
}

func TestNohupHelpVersion(t *testing.T) {
	requireHelp(t, "nohup", []string{"--help"}, 0)
	requireHelp(t, "nohup", []string{"--hel"}, 0)
	requireHelp(t, "nohup", []string{"--h"}, 0)
	requireHelp(t, "nohup", []string{"--help", "extra"}, 0)
	requireVersion(t, "nohup", []string{"--version"}, 0)
	requireVersion(t, "nohup", []string{"--ver"}, 0)
	requireVersion(t, "nohup", []string{"--v"}, 0)
	requireVersion(t, "nohup", []string{"--version", "extra"}, 0)
}
