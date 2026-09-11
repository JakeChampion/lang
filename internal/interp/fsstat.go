package interp

// rawFsStat is `statfs(2)`'s record in the shape `FsStat` exposes. The two
// limits are filled from whichever place the platform keeps them — Linux's
// `f_namelen` plus its PATH_MAX constant, Darwin's `pathconf(2)` — and
// `fsStatFields` is split per GOOS because the two `syscall.Statfs_t`
// declarations share neither field names nor field set.
type rawFsStat struct {
	blockSize, blocks, blocksFree, blocksAvail int64
	files, filesFree                           int64
	nameMax, pathMax                           int64
}
