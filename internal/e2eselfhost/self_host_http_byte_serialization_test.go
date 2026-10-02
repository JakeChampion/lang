package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostHTTPByteSerialization(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "serialize.fern")
	if err := os.WriteFile(src, []byte(e2eharness.HTTPByteSerializationProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	_, targets, _ := hostTargets()
	if _, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{"wasmtime", "run"}})
	}
	for _, compiler := range []struct{ name, cli string }{{"primary", cli}, {"bootstrap", bootstrap}} {
		t.Run(compiler.name+"/interpreter", func(t *testing.T) {
			args := []string{"-interp", src}
			if compiler.name == "primary" {
				args = append(args, stdlib)
			}
			e2eharness.CheckHTTPByteSerialization(t, exec.Command(compiler.cli, args...))
		})
		for _, target := range targets {
			forms := []string{""}
			if target.target == "wasm32-wasi" {
				forms = append(forms, "core")
			}
			for _, form := range forms {
				t.Run(compiler.name+"/"+target.target+"/"+form, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), "serialize")
					args := []string{"-target", target.target, "-o", bin}
					if form == "core" {
						emit := "command-module"
						if compiler.name == "primary" {
							emit = "core-module"
						}
						args = append(args, "-emit", emit)
					}
					args = append(args, src)
					if compiler.name == "primary" {
						args = append(args, stdlib)
					}
					compile := exec.Command(compiler.cli, args...)
					if compiler.name == "primary" {
						compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					}
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					diagnostic := e2eharness.CheckHTTPByteSerialization(t, runX86_64Bin(target.runner, bin))
					if compiler.name == "primary" && (target.target != "wasm32-wasi" || form == "core") {
						assertBalancedCensus(t, diagnostic)
					}
				})
			}
		}
	}
}
