package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostTomlCRLF(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/toml_crlf_run.fern")

	const want = "LF   name=acme\n" +
		"LF   deps=2\n" +
		"LF   dep dbl kind=path version= path=../dbl\n" +
		"LF   dep shout kind=version version=1.2.3 path=\n" +
		"LF   member a\n" +
		"LF   member b#c\n" +
		"CRLF name=acme\n" +
		"CRLF deps=2\n" +
		"CRLF dep dbl kind=path version= path=../dbl\n" +
		"CRLF dep shout kind=version version=1.2.3 path=\n" +
		"CRLF member a\n" +
		"CRLF member b#c\n" +
		"LF   entries=2\n" +
		"LF   entry dbl version=1.0.0 path=../dbl hash=\n" +
		"LF   entry shout version=1.2.3 path= hash=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"CRLF entries=2\n" +
		"CRLF entry dbl version=1.0.0 path=../dbl hash=\n" +
		"CRLF entry shout version=1.2.3 path= hash=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"

	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "drivers/toml_crlf_run.fern", "toml_crlf_run", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("toml_crlf_run: %v\n%s", err, out)
			}
			if got := string(out); got != want {
				t.Errorf("CRLF manifest/lock parse mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}
