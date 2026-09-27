package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// SyscallFloorProbe exercises the native runtime's raw floor from a program:
// `__syscall4` opens a file, `__syscall6` maps it at a nonzero offset (which
// makes every operand observable, since offset zero maps different bytes),
// `__load_u8` reads the mapped bytes, `__syscall3` unmaps and closes, a
// negative descriptor pins the signed -errno result, and `__store_u8` writes
// two bytes into an allocated block that `__load_u8` reads back. The numbers
// are the target's: x86-64-linux, arm64-linux or arm64-darwin. Exit 0 iff
// every step holds; each failing step has its own code.
func SyscallFloorProbe(t *testing.T, dir, target string) string {
	t.Helper()
	const page = 65536 // aligned on both 4 KiB and 16 KiB page hosts
	data := make([]byte, page*2)
	data[page], data[len(data)-1] = 91, 37
	path := filepath.Join(dir, "mapped.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	open, atcwd, mmap, munmap, close := 257, -100, 9, 11, 3
	switch target {
	case "arm64-linux":
		open, mmap, munmap, close = 56, 222, 215, 57
	case "arm64-darwin":
		open, atcwd, mmap, munmap, close = 463, -2, 197, 73, 6
	}
	return fmt.Sprintf(`function main(): i32 {
    var path: string = "%s";
    var cpath: usize = __alloc(path.len() + 1);
    var i: i32 = 0;
    while (i < path.len()) {
        __store_u8(cpath + (i as usize), path[i] as i32);
        i = i + 1;
    }
    __store_u8(cpath + (path.len() as usize), 0);
    var fd: i64 = __syscall4(%d, %d, cpath as i64, 0, 0);
    if (fd < 0) { return 1; }
    var p: i64 = __syscall6(%d, 0, 65536, 1, 2, fd, 65536);
    if (p <= 0) { return 2; }
    var first: i32 = __load_u8(p as usize);
    var last: i32 = __load_u8((p as usize) + 65535);
    var unmapped: i64 = __syscall3(%d, p, 65536, 0);
    var closed: i64 = __syscall3(%d, fd, 0, 0);
    if (first != 91 || last != 37) { return 3; }
    if (unmapped != 0 || closed != 0) { return 4; }
    if (__syscall6(%d, 0, 65536, 1, 2, 0 - 1, 65536) != 0 - 9) { return 5; }
    var block: usize = __alloc(16);
    __store_u8(block, 200);
    __store_u8(block + 15, 263);
    if (__load_u8(block) != 200 || __load_u8(block + 15) != 7) { return 6; }
    return 0;
}
`, path, open, atcwd, mmap, munmap, close, mmap)
}
