package e2eharness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// CLIBuild is a program that the self-host CLI for this tree compiles.
type CLIBuild struct {
	Target string
	Dir    string // holds Entry and the files it imports by relative path
	Entry  string
	Embed  string // the -embed directory, or empty
	Args   []string
	Env    []string // the FERN_* settings of the compile
}

var cliBuildCache = newBuildCache[string]()

// CachedCLIBuild compiles b with SelfHostCLI and keeps the binary in the build
// cache. The key covers the CLI's bytes and each input the compile reads, so
// a hit is the binary the compile would write.
func CachedCLIBuild(t testing.TB, b CLIBuild) string {
	t.Helper()
	cli := SelfHostCLI(t)
	key := cliBuildKey(t, cli, b)
	path, err := cliBuildCache.get(key, func() (string, error) {
		return cachedBinary(t, "cli-"+key, key+".clibin", func(bin string) error {
			args := append([]string{"-target", b.Target}, b.Args...)
			if b.Embed != "" {
				args = append(args, "-embed", b.Embed)
			}
			args = append(args, "-o", bin, filepath.Join(b.Dir, b.Entry), SelfHostStdlibRoot(t))
			return withBuildMemory(DriverBuildWeightMB(b.Entry), func() error {
				cmd := exec.Command(cli, args...)
				cmd.Env = ChildEnv(b.Env...)
				if out, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("self-host CLI build of %s for %s: %v\n%s", b.Entry, b.Target, err, out)
				}
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func cliBuildKey(t testing.TB, cli string, b CLIBuild) string {
	t.Helper()
	h := sha256.New()
	fmt.Fprintf(h, "cli-build-v1\x00%s\x00%s\x00%s\x00%s\x00", fileSHA256(t, cli), b.Target, b.Entry, HashSelfHostSources(t, b.Dir, b.Entry))
	var env []string
	for _, kv := range ChildEnv(b.Env...) {
		if strings.HasPrefix(kv, "FERN_") {
			env = append(env, kv)
		}
	}
	fmt.Fprintf(h, "args\x00%s\x00env\x00%s\x00", strings.Join(b.Args, "\x00"), strings.Join(env, "\x00"))
	if b.Embed != "" {
		embed, err := treeHash(b.Embed)
		if err != nil {
			t.Fatalf("hash %s: %v", b.Embed, err)
		}
		fmt.Fprintf(h, "embed\x00%s\x00", embed)
	}
	fmt.Fprintf(h, "stdlib\x00%s\x00", stdlibHash(t))
	return hex.EncodeToString(h.Sum(nil))[:32]
}
