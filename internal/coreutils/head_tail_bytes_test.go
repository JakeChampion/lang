package coreutils

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestHeadTailBytesParity(t *testing.T) {
	for _, utility := range []string{"head", "tail"} {
		t.Run(utility, func(t *testing.T) {
			dir := t.TempDir()
			var cases []invocation
			for _, tc := range e2eharness.HeadTailByteCases(utility) {
				args := append([]string{}, tc.Args...)
				input := tc.Input
				if tc.File {
					args = append(args, writeFile(t, dir, tc.Name, input))
					input = ""
				}
				cases = append(cases, invocation{name: tc.Name, args: args, stdin: input})
			}
			if utility == "tail" {
				path := writeFile(t, dir, "follow", "\xff\n")
				cases = append(cases, invocation{name: "raw follow append", args: []string{"---disable-inotify", "-s", "0.05", "-f", "-n", "1", path}, limit: 5,
					follow: []followStep{{after: 2, act: "append", path: path, data: "\x80\xc0\n"}}})
			}
			requireParity(t, utility, cases)
		})
	}
}
