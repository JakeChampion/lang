package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const strCmpWordsProg = `import "std/string";

function slow(a: string, b: string): i32 {
    let ab: u8[] = a.bytes();
    let bb: u8[] = b.bytes();
    let n: i32 = ab.len();
    if (bb.len() < n) { n = bb.len(); }
    let i: i32 = 0;
    while (i < n) {
        if (ab[i] != bb[i]) {
            if (ab[i] < bb[i]) { return 0 - 1; }
            return 1;
        }
        i = i + 1;
    }
    if (ab.len() < bb.len()) { return 0 - 1; }
    if (ab.len() > bb.len()) { return 1; }
    return 0;
}

function agrees(a: string, b: string): boolean {
    let want: i32 = slow(a, b);
    return (a < b) == (want < 0) && (a <= b) == (want <= 0) && (a > b) == (want > 0)
        && (a >= b) == (want >= 0) && (a == b) == (want == 0);
}

function main(): i32 {
    let bases: string[] = ["abcdefghijklmnopqrst", "file1234.txt-and-more"];
    let alts: string[] = ["A", "z", "é", "~"];
    let all: string[] = [""];
    for base in bases {
        let n: i32 = base.len();
        let cut: i32 = 0;
        while (cut <= n) {
            all = all.append(base.take(cut));
            cut = cut + 1;
        }
        let pos: i32 = 0;
        while (pos < 20 && pos < n) {
            for alt in alts {
                all = all.append(base.take(pos) + alt + base.drop(pos + 1));
                all = all.append("é" + base.take(pos) + alt);
            }
            pos = pos + 1;
        }
    }
    let i: i32 = 0;
    while (i < all.len()) {
        let j: i32 = 0;
        while (j < all.len()) {
            if (!agrees(all[i], all[j])) {
                print(all[i] + " | " + all[j] + "\n");
                return 1;
            }
            j = j + 1;
        }
        i = i + 1;
    }
    return 0;
}
`

func TestSelfHostStrCmpWords(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "cmp.fern")
	if err := os.WriteFile(src, []byte(strCmpWordsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		out, _ := run.Output()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, ordering disagreed with the byte loop on %q", tg.target, got, out)
		}
	}
}
