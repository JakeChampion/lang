package e2e

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"hash/adler32"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// dexerClasses compiles the Java program of tests/android/dexer for Java 8,
// and returns the paths of its class files.
func dexerClasses(t *testing.T) (string, []string) {
	t.Helper()
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac not on PATH")
	}
	classes := t.TempDir()
	src := e2eharness.RepoPath("tests", "android", "dexer", "dev", "fern", "dexer", "Main.java")
	if out, err := exec.Command(javac, "--release", "8", "-nowarn", "-d", classes, src).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	files, err := filepath.Glob(filepath.Join(classes, "dev", "fern", "dexer", "*.class"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no class files in %s: %v", classes, err)
	}
	return classes, files
}

func runDex(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
	cmd.Env = e2eharness.ChildEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The JVM output of the program is tests/android/dexer/expected.txt, the
// output that the emulator lane requires of ART for the DEX file.
func TestDexerExpectedOutput(t *testing.T) {
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not on PATH")
	}
	classes, _ := dexerClasses(t)
	cmd := exec.Command(java, "-cp", classes, "dev.fern.dexer.Main")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("java: %v", err)
	}
	want, err := os.ReadFile(e2eharness.RepoPath("tests", "android", "dexer", "expected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != string(want) {
		t.Errorf("the JVM output is not expected.txt:\n%s", stdout.String())
	}
}

// fern -dex writes one DEX file with each class, the same from the class
// files and from a JAR of them. The header has the size, the checksum and
// the signature of the file. The Android lane runs D8 on the file.
func TestDexerWritesDex(t *testing.T) {
	_, files := dexerClasses(t)
	dir := t.TempDir()
	fromClasses := filepath.Join(dir, "classes.dex")
	if out, err := runDex(t, append([]string{"-dex", fromClasses}, files...)...); err != nil {
		t.Fatalf("fern -dex: %v\n%s", err, out)
	}
	jar := filepath.Join(dir, "main.jar")
	writeJar(t, jar, files)
	fromJar := filepath.Join(dir, "jar.dex")
	if out, err := runDex(t, "-dex", fromJar, jar); err != nil {
		t.Fatalf("fern -dex of a JAR: %v\n%s", err, out)
	}
	a, err := os.ReadFile(fromClasses)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(fromJar)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("the DEX file of the JAR is not the DEX file of its class files")
	}
	checkDexHeader(t, a, len(files))
}

// writeJar writes the class files to a JAR at their paths in the package,
// with a manifest and a module-info.class that fern -dex must skip.
func writeJar(t *testing.T, path string, files []string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	add("META-INF/MANIFEST.MF", []byte("Manifest-Version: 1.0\n"))
	add("module-info.class", []byte("not a class"))
	add("META-INF/versions/9/dev/fern/dexer/Main.class", []byte("not a class"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		add("dev/fern/dexer/"+filepath.Base(f), data)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkDexHeader(t *testing.T, d []byte, classes int) {
	t.Helper()
	if len(d) < 0x70 || string(d[:8]) != "dex\n035\x00" {
		t.Fatalf("no DEX 035 header: % x", d[:min(len(d), 8)])
	}
	le := binary.LittleEndian
	if got := le.Uint32(d[32:]); int(got) != len(d) {
		t.Errorf("file_size %d, want %d", got, len(d))
	}
	if got, want := le.Uint32(d[8:]), adler32.Checksum(d[12:]); got != want {
		t.Errorf("checksum %08x, want %08x", got, want)
	}
	if got, want := d[12:32], sha1.Sum(d[32:]); !bytes.Equal(got, want[:]) {
		t.Errorf("signature % x, want % x", got, want)
	}
	if got := le.Uint32(d[96:]); int(got) != classes {
		t.Errorf("class_defs_size %d, want %d", got, classes)
	}
}

// fern -dex refuses a command with no input, and a file that is not a class
// file, with the name of the file.
func TestDexerRefusals(t *testing.T) {
	dir := t.TempDir()
	out, err := runDex(t, "-dex", filepath.Join(dir, "out.dex"))
	if err == nil || !strings.Contains(out, "-dex OUT takes one or more class files or JAR files") {
		t.Errorf("want the refusal of no input, got %v:\n%s", err, out)
	}
	bad := filepath.Join(dir, "Bad.class")
	if err := os.WriteFile(bad, []byte("not a class"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runDex(t, "-dex", filepath.Join(dir, "out.dex"), bad)
	if err == nil || !strings.Contains(out, "fern: -dex: ") {
		t.Errorf("want the refusal of a bad class file, got %v:\n%s", err, out)
	}
}
