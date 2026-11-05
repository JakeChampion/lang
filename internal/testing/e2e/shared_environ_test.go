package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const sharedEnvironSource = `pub function state(): i32 {
  match (env("FERN_SHARED_ENV_PROBE")) {
    Some(v) => {
      if (v == "before") { return 11; }
      if (v == "after") { return 12; }
      if (v.len() == 0) { return 13; }
      return 14;
    },
    None => { return 10; }
  }
}

pub function visible(): i32 {
  for value in environ() {
    if (value == "FERN_SHARED_ENV_PROBE=before" || value == "FERN_SHARED_ENV_PROBE=after" || value == "FERN_SHARED_ENV_PROBE=") {
      return 1;
    }
  }
  return 0;
}

function main(): i32 { return 0; }
`

const sharedEnvironLoader = `#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
int main(int argc, char **argv) {
  const char *key = "FERN_SHARED_ENV_PROBE";
  if (setenv(key, "before", 1)) return 100;
  void *h = dlopen(argv[1], RTLD_NOW);
  if (!h) { fprintf(stderr, "%s\n", dlerror()); return 101; }
  int (*state)(void) = (int (*)(void))dlsym(h, "state");
  int (*visible)(void) = (int (*)(void))dlsym(h, "visible");
  if (!state || !visible) return 102;
  if (state() != 11 || visible() != 1) return 1;
  if (setenv(key, "after", 1)) return 103;
  if (state() != 12 || visible() != 1) return 2;
  if (setenv(key, "", 1)) return 104;
  if (state() != 13 || visible() != 1) return 3;
  if (unsetenv(key)) return 105;
  if (state() != 10 || visible() != 0) return 4;
  if (clearenv()) return 106;
  if (state() != 10 || visible() != 0) return 5;
  if (setenv(key, "after", 1)) return 107;
  if (state() != 12 || visible() != 1) return 6;
  dlclose(h);
  return 42;
}
`

func TestSharedEnvironELFImports(t *testing.T) {
	for _, tc := range []struct {
		target string
		libc   string
		glob   uint32
	}{
		{"x86-64-linux", "libc.so.6", uint32(elf.R_X86_64_GLOB_DAT)},
		{"arm64-linux", "libc.so.6", uint32(elf.R_AARCH64_GLOB_DAT)},
		{"x86-64-android", "libc.so", uint32(elf.R_X86_64_GLOB_DAT)},
		{"arm64-android", "libc.so", uint32(elf.R_AARCH64_GLOB_DAT)},
	} {
		t.Run(tc.target, func(t *testing.T) {
			so, out := buildImportLib(t, tc.target, sharedEnvironSource, "state", "visible")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			raw, err := os.ReadFile(so)
			if err != nil {
				t.Fatal(err)
			}
			f, err := elf.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			libs, err := f.ImportedLibraries()
			if err != nil || len(libs) != 1 || libs[0] != tc.libc {
				t.Fatalf("DT_NEEDED = %v (%v), want [%s]", libs, err, tc.libc)
			}
			syms, err := f.DynamicSymbols()
			if err != nil {
				t.Fatal(err)
			}
			index := uint64(0)
			for i, sym := range syms {
				if sym.Name == "environ" && sym.Section == elf.SHN_UNDEF {
					if elf.ST_TYPE(sym.Info) != elf.STT_OBJECT {
						t.Fatalf("environ type = %s, want OBJECT", elf.ST_TYPE(sym.Info))
					}
					index = uint64(i + 1)
				}
			}
			if index == 0 {
				t.Fatal("no undefined environ object")
			}
			rela := f.Section(".rela.dyn")
			if rela == nil {
				t.Fatal("no dynamic relocations")
			}
			relocs, err := rela.Data()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for i := 0; i+24 <= len(relocs); i += 24 {
				info := f.ByteOrder.Uint64(relocs[i+8:])
				found = found || info>>32 == index && uint32(info) == tc.glob
			}
			if !found {
				t.Fatal("no GLOB_DAT relocation for environ")
			}
		})
	}
}

func TestSharedEnvironTracksHost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the loader requires Linux")
	}
	for _, tc := range []struct {
		target string
		arch   string
		cc     string
		qemu   string
		prefix string
	}{
		{"x86-64-linux", "amd64", "x86_64-linux-gnu-gcc", "qemu-x86_64", "/usr/x86_64-linux-gnu"},
		{"arm64-linux", "arm64", "aarch64-linux-gnu-gcc", "qemu-aarch64", "/usr/aarch64-linux-gnu"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			cc, runner := tc.cc, tc.qemu
			if runtime.GOARCH == tc.arch {
				cc, runner = "gcc", ""
			}
			if _, err := exec.LookPath(cc); err != nil {
				t.Skipf("no %s", cc)
			}
			if runner != "" {
				if _, err := exec.LookPath(runner); err != nil {
					t.Skipf("no %s", runner)
				}
			}
			so, out := buildImportLib(t, tc.target, sharedEnvironSource, "state", "visible")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			dir := t.TempDir()
			cpath, loader := filepath.Join(dir, "load.c"), filepath.Join(dir, "load")
			if err := os.WriteFile(cpath, []byte(sharedEnvironLoader), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(cc, cpath, "-ldl", "-o", loader).CombinedOutput(); err != nil {
				t.Fatalf("build loader: %v\n%s", err, out)
			}
			cmd := exec.Command(loader, so)
			if runner != "" {
				cmd = exec.Command(runner, loader, so)
				cmd.Env = append(os.Environ(), "QEMU_LD_PREFIX="+tc.prefix)
			}
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 42 {
				t.Fatalf("host environment changes were not visible: %v\n%s", err, out)
			}
		})
	}
}
