package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func TestArm64ReadFilePathLengthSweep(t *testing.T) {
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
