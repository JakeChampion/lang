package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

// hostGLLibs are the libraries that a Fern shared object for std/android/gl
// needs, under their Android names, and the Linux files that provide them.
// libc.so is the Android name that std/android imports libc by.
var hostGLLibs = [][2]string{
	{"libEGL.so", "libEGL.so.1"},
	{"libGLESv2.so", "libGLESv2.so.2"},
	{"libc.so", "libc.so.6"},
}

// hostWindowStub is libandroid.so for a host with no Android windows. Its
// window functions report a window that takes no surface, so a canvas that
// tries to attach to one stays detached.
const hostWindowStub = `int ANativeWindow_setBuffersGeometry(void* w, int width, int height, int format) { return -1; }
int ANativeWindow_getWidth(void* w) { return 0; }
int ANativeWindow_getHeight(void* w) { return 0; }
`

// LibandroidStub is C source for libandroid.so on a Linux host: the
// functions in `defined`, and from scripts/libandroid-stubs a function that
// aborts for each other libandroid.so function that `src` or a std/android
// module imports.
func LibandroidStub(t *testing.T, defined, src string) string {
	t.Helper()
	dir := t.TempDir()
	mock := filepath.Join(dir, "defined.c")
	source := filepath.Join(dir, "source.fern")
	if err := os.WriteFile(mock, []byte(defined), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", RepoPath("scripts", "libandroid-stubs"), mock, source).Output()
	if err != nil {
		t.Fatalf("scripts/libandroid-stubs: %v", err)
	}
	return string(out) + defined
}

// HostGL gives a directory to put on LD_LIBRARY_PATH so that a Fern shared
// object for std/android/gl loads on this Linux host, with Mesa's EGL and
// OpenGL ES 2 under their Android names, and a stub libandroid.so. It skips
// the test when Mesa or gcc is not installed, and fails it when
// FERN_REQUIRE_HOST_GL is set.
func HostGL(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("std/android/gl runs on the host only on Linux, not %s", runtime.GOOS)
	}
	dir := t.TempDir()
	var missing []string
	for _, lib := range hostGLLibs {
		path := findSystemLib(lib[1])
		if path == "" {
			missing = append(missing, lib[1])
			continue
		}
		if err := os.Symlink(path, filepath.Join(dir, lib[0])); err != nil {
			t.Fatal(err)
		}
	}
	if findSystemLib("libEGL_mesa.so.0") == "" {
		missing = append(missing, "libEGL_mesa.so.0")
	}
	if cc, err := exec.LookPath("gcc"); err != nil {
		missing = append(missing, "gcc")
	} else {
		src := filepath.Join(dir, "android_stub.c")
		if err := os.WriteFile(src, []byte(LibandroidStub(t, hostWindowStub, "")), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", filepath.Join(dir, "libandroid.so"), src).CombinedOutput(); err != nil {
			t.Fatalf("build the libandroid.so stub: %v\n%s", err, out)
		}
	}
	if len(missing) > 0 {
		msg := "no Mesa EGL and OpenGL ES 2 on this host (" + strings.Join(missing, ", ") + "): install gcc, libegl1, libgles2 and libegl-mesa0"
		if envflag.On("FERN_REQUIRE_HOST_GL") {
			t.Fatalf("%s, and FERN_REQUIRE_HOST_GL=1", msg)
		}
		t.Skip(msg)
	}
	return dir
}

// HostGLEnv is the environment of a program that loads a library through
// the directory of HostGL: Mesa draws with no display server.
func HostGLEnv(dir string) []string {
	return append(os.Environ(), "LD_LIBRARY_PATH="+dir, "EGL_PLATFORM=surfaceless")
}

func findSystemLib(name string) string {
	out, err := exec.Command("ldconfig", "-p").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[0] == name && fields[len(fields)-2] == "=>" {
				return fields[len(fields)-1]
			}
		}
	}
	for _, d := range []string{"/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/lib/x86_64-linux-gnu", "/lib/aarch64-linux-gnu", "/usr/lib64", "/usr/lib"} {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
