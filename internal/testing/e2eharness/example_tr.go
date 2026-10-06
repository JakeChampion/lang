package e2eharness

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// CheckExampleTr exercises the example's byte stream, including invalid UTF-8
// and squeeze state spanning read_chunk_bytes calls.
func CheckExampleTr(t *testing.T, command func(...string) *exec.Cmd, census bool) {
	t.Helper()
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	mapped := bytes.ReplaceAll(all, []byte{'a'}, []byte{'Z'})
	for _, tc := range []struct {
		name        string
		args        []string
		input, want []byte
	}{
		{"binary identity", []string{"", ""}, all, all},
		{"binary translate", []string{"a", "Z"}, all, mapped},
		{"ranges", []string{"a-z", "A-Z"}, []byte("hello\x00\xff"), []byte("HELLO\x00\xff")},
		{"delete", []string{"-d", "a"}, all, bytes.ReplaceAll(all, []byte{'a'}, nil)},
		{"squeeze across chunks", []string{"-s", "a"}, append(bytes.Repeat([]byte{'a'}, 8193), 0xff, 0xff), []byte{'a', 0xff, 0xff}},
		{"utf8 operand bytes", []string{"é", "X"}, []byte("é\xff"), []byte("XX\xff")},
		{"first duplicate", []string{"aba", "XYZ"}, []byte("aba"), []byte("XYX")},
		{"last replacement repeats", []string{"abc", "XY"}, []byte("abc"), []byte("XYY")},
		{"empty replacement", []string{"a", ""}, []byte("aba\xff"), []byte("b\xff")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := command(tc.args...)
			cmd.Stdin = bytes.NewReader(tc.input)
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, diagnostic.String())
			}
			if !bytes.Equal(out.Bytes(), tc.want) {
				t.Fatalf("output = %x, want %x\n%s", out.Bytes(), tc.want, diagnostic.String())
			}
			if census && (strings.Contains(diagnostic.String(), "fern-sanitizer:") || !strings.Contains(diagnostic.String(), "live_bytes=0")) {
				t.Fatalf("missing clean ownership census\n%s", diagnostic.String())
			}
		})
	}
}
