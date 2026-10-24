package interp

type rawFsStat struct {
	blockSize, blocks, blocksFree, blocksAvail int64
	files, filesFree                           int64
	nameMax, pathMax                           int64
	fsType, fsid, fragSize                     int64
	fsTypeName                                 string
}

func fsidWord(val [2]int32) int64 {
	return int64(uint64(uint32(val[0]))<<32 | uint64(uint32(val[1])))
}
