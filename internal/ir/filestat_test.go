package ir

import "testing"

// The four backends share one offset table because FileStat happens to
// lay out identically on 8-byte and 4-byte pointer targets: nothing in it
// is pointer-shaped, so `boolean` and `u32` take 4 bytes everywhere and
// `i64` takes 8. Add a `string`, a slice or a nested struct to the
// declaration and that stops holding — wasm32 would put it at a different
// offset than the natives — so this fails rather than letting wasmbin
// write the whole tail of the struct to the wrong addresses.
func TestFileStatLayoutIsTargetIndependent(t *testing.T) {
	if got, want := fileStatLayout(4), FileStat; got != want {
		t.Errorf("FileStat lays out differently on wasm32:\n\tptrW=4: %+v\n\tptrW=8: %+v\n"+
			"the backends share one table, so a pointer-shaped field needs per-target offsets first",
			got, want)
	}
}

// Pin the actual numbers. The layout is an ABI between the checker's
// declaration and hand-written assembly in four backends plus the
// self-host's own copy of the struct, so a field reordered or retyped in
// the declaration has to be a deliberate, visible diff here.
func TestFileStatOffsets(t *testing.T) {
	want := FileStatLayout{
		IsFile: 0, IsDir: 4, Size: 8,
		Mode: 16, Nlink: 20, UID: 24, GID: 28,
		Dev: 32, Rdev: 40, Ino: 48, Blksize: 56, Blocks: 64,
		Atime: 72, AtimeNsec: 80,
		Mtime: 88, MtimeNsec: 96,
		Ctime: 104, CtimeNsec: 112,
		Bytes: 120,
	}
	if FileStat != want {
		t.Errorf("FileStat layout moved:\n\tgot  %+v\n\twant %+v", FileStat, want)
	}
}
