package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func SyscallFloorProbe(t *testing.T, dir, target string) string {
	t.Helper()
	const page = 65536
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
    let path: string = "%s";
    let cpath: usize = __alloc(path.len() + 1);
    let i: i32 = 0;
    while (i < path.len()) {
        __store_u8(cpath + (i as usize), path[i] as i32);
        i = i + 1;
    }
    __store_u8(cpath + (path.len() as usize), 0);
    let fd: i64 = __syscall4(%d, %d, cpath as i64, 0, 0);
    if (fd < 0) { return 1; }
    let p: i64 = __syscall6(%d, 0, 65536, 1, 2, fd, 65536);
    if (p <= 0) { return 2; }
    let first: i32 = __load_u8(p as usize);
    let last: i32 = __load_u8((p as usize) + 65535);
    let unmapped: i64 = __syscall3(%d, p, 65536, 0);
    let closed: i64 = __syscall3(%d, fd, 0, 0);
    if (first != 91 || last != 37) { return 3; }
    if (unmapped != 0 || closed != 0) { return 4; }
    if (__syscall6(%d, 0, 65536, 1, 2, 0 - 1, 65536) != 0 - 9) { return 5; }
    let block: usize = __alloc(16);
    __store_u8(block, 200);
    __store_u8(block + 15, 263);
    if (__load_u8(block) != 200 || __load_u8(block + 15) != 7) { return 6; }
    return 0;
}
`, path, open, atcwd, mmap, munmap, close, mmap)
}
