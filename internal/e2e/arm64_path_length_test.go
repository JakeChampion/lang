package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// TestArm64ReadFilePathLengthSweep guards the path-NUL-termination
// fix in the arm64 Go backend's __fern_read_file_2W: a path of
// exactly 0 mod 16 bytes lands in a same-size bump-heap slot with
// no zero pad, so without an explicit NUL terminator the kernel
// reads past the intended end into whatever's at the next slot.
// In the wild that next slot is usually the second argv string,
// concatenating two paths into one — openat sees a non-existent
// path and the program silently fails.
//
// Existing TestArm64ReadFileOk happens to use "greeting.txt" (12
// bytes) — non-0-mod-16, hits the implicit-NUL alignment padding,
// passes by accident. This test sweeps path lengths 12..49
// (covering 16 / 32 / 48 — every 0-mod-16 value in that range) so
// a regression to the helper trips quickly.
//
// SKIPs cleanly when the aarch64 cross-toolchain isn't installed.
func TestArm64ReadFilePathLengthSweep(t *testing.T) {
	// Path layout: "ABCD…ABCDEFGH" + ".txt" — a fixed suffix
	// + variable prefix so we can pin total length precisely.
	for _, total := range []int{12, 16, 17, 31, 32, 33, 47, 48, 49} {
		t.Run(fmt.Sprintf("len%d", total), func(t *testing.T) {
			if total < 5 {
				t.Fatal("test bug: path too short for .txt suffix")
			}
			prefix := strings.Repeat("A", total-4)
			path := prefix + ".txt"
			content := "hello"
			src := fmt.Sprintf(`function main(): i32 {
    match (read_file("%s")) {
        Ok(s) => { return s.len(); },
        Err(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`, path)
			_, code, _ := compileArm64InDir(t, src, map[string]string{
				path: content,
			})
			if code != len(content) {
				t.Errorf("path len %d (%q): got exit %d, want %d (Ok content len)",
					total, path, code, len(content))
			}
		})
	}
}

// TestArm64WriteFilePathLengthSweep is the write_file counterpart.
// Same NUL-term bug applies: a 0-mod-16 path concatenates with
// whatever's adjacent in the bump heap and openat (O_WRONLY|
// O_CREAT|O_TRUNC) creates a file at the WRONG path — or fails
// outright if the resolved path is invalid.
func TestArm64WriteFilePathLengthSweep(t *testing.T) {
	for _, total := range []int{12, 16, 17, 31, 32, 33, 47, 48, 49} {
		t.Run(fmt.Sprintf("len%d", total), func(t *testing.T) {
			if total < 5 {
				t.Fatal("test bug: path too short for .txt suffix")
			}
			prefix := strings.Repeat("B", total-4)
			path := prefix + ".txt"
			content := "wrote"
			src := fmt.Sprintf(`function main(): i32 {
    match (write_file("%s", "%s")) {
        Ok(_) => {
            match (read_file("%s")) {
                Ok(s) => { return s.len(); },
                Err(_) => { return 0 - 3; }
            }
        },
        Err(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`, path, content, path)
			_, code, _ := compileArm64InDir(t, src, nil)
			if code != len(content) {
				t.Errorf("path len %d (%q): got exit %d, want %d (write→read roundtrip len)",
					total, path, code, len(content))
			}
		})
	}
}
