package main

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

func TestShouldColorizeAuto(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devnull.Close()
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer pipeR.Close()
	defer pipeW.Close()
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	t.Setenv("NO_COLOR", "")
	for _, tc := range []struct {
		name string
		f    *os.File
		want bool
	}{
		{"a terminal", slave, true},
		{"a pipe", pipeW, false},
		{"/dev/null", devnull, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := os.Stderr
			os.Stderr = tc.f
			got := shouldColorize("auto")
			os.Stderr = saved
			if got != tc.want {
				t.Errorf("shouldColorize(auto) with stderr on %s = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestShouldColorizeOverridesIgnoreTheStream(t *testing.T) {
	if !shouldColorize("always") {
		t.Error(`shouldColorize("always") = false`)
	}
	if shouldColorize("never") {
		t.Error(`shouldColorize("never") = true`)
	}
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	t.Setenv("NO_COLOR", "1")
	saved := os.Stderr
	os.Stderr = slave
	got := shouldColorize("auto")
	os.Stderr = saved
	if got {
		t.Error("NO_COLOR did not turn colour off on a terminal")
	}
}
