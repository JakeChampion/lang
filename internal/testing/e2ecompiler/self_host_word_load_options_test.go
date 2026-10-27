package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var wordLoadOptionCases = []struct{ name, expression string }{
	{"little", "bs[o] as u32 | bs[o+1] as u32 << 8 | bs[o+2] as u32 << 16 | bs[o+3] as u32 << 24"},
	{"big", "bs[o] as u32 << 24 | bs[o+1] as u32 << 16 | bs[o+2] as u32 << 8 | bs[o+3] as u32"},
	{"duplicate", "bs[o] as u32 | bs[o+1] as u32 << 8 | bs[o+1] as u32 << 16 | bs[o+3] as u32 << 24"},
	{"gap", "bs[o] as u32 | bs[o+1] as u32 << 8 | bs[o+2] as u32 << 16 | bs[o+4] as u32 << 24"},
	{"mixed-array", "bs[o] as u32 | bs[o+1] as u32 << 8 | cs[o+2] as u32 << 16 | bs[o+3] as u32 << 24"},
	{"mixed-index", "bs[o] as u32 | bs[o+1] as u32 << 8 | bs[j+2] as u32 << 16 | bs[o+3] as u32 << 24"},
	{"wrong-shift", "bs[o] as u32 | bs[o+1] as u32 << 8 | bs[o+2] as u32 << 17 | bs[o+3] as u32 << 24"},
	{"short", "bs[o] as u32 | bs[o+1] as u32 << 8 | bs[o+2] as u32 << 16"},
	{"negative-offset", "bs[o+-1] as u32 | bs[o] as u32 << 8 | bs[o+1] as u32 << 16 | bs[o+2] as u32 << 24"},
	{"scrambled", "bs[o+2] as u32 << 16 | bs[o] as u32 | bs[o+3] as u32 << 24 | bs[o+1] as u32 << 8"},
}

func TestSelfHostWordLoadOptions(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range wordLoadOptionCases {
		for _, ty := range []string{"u8[]", "[u8]"} {
			t.Run(tc.name+"/"+ty, func(t *testing.T) {
				source := fmt.Sprintf(`@noinline function probe(bs:%s,cs:%s,o:i32,j:i32):u32 {return %s;}
function main():i32 {
 let bs:u8[]=[1u8,2u8,3u8,4u8,5u8,6u8,7u8,8u8];
 let cs:u8[]=[11u8,12u8,13u8,14u8,15u8,16u8,17u8,18u8];
 return (probe(bs,cs,2,1)&127u32) as i32;
}`, ty, ty, tc.expression)
				path := filepath.Join(t.TempDir(), "word.fern")
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(oracle, "-check", path).CombinedOutput(); err != nil || len(out) != 0 {
					t.Fatalf("Go check: %v\n%s", err, out)
				}
				want := interpExit(t, oracle, source)
				for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
					t.Run(target, func(t *testing.T) {
						stderr, got := cli.exitOf(t, source, target)
						if got != want || stderr != "" {
							t.Fatalf("exit %d, want %d\n%s", got, want, stderr)
						}
					})
				}
			})
		}
	}
}
