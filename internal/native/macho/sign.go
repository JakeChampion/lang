package macho

import (
	"crypto/sha256"
	"encoding/binary"
)

// Code-signing blob magics and constants (big-endian on disk).
const (
	csSuperBlobMagic = 0xFADE0CC0 // CSMAGIC_EMBEDDED_SIGNATURE
	csCodeDirMagic   = 0xFADE0C02 // CSMAGIC_CODEDIRECTORY

	csslotCodeDirectory = 0 // CSSLOT_CODEDIRECTORY

	csVersion        = 0x20400 // supports execSeg fields
	csAdhoc          = 0x00000002
	csHashTypeSHA256 = 2
	csHashSize       = 32
	csLog2PageSize   = 12 // 4096-byte signing pages

	csExecSegMainBinary = 0x1

	cdHeaderLen = 88 // CodeDirectory header through execSegFlags (version 0x20400)
)

// codeSignature builds an ad-hoc CSMAGIC_EMBEDDED_SIGNATURE SuperBlob
// containing a single CodeDirectory whose code-slot hashes are SHA-256 of
// each 4 KiB page of content (file[0:codeLimit]). When content is nil it
// returns a same-sized blob with zero hashes (a layout/size probe).
// execSegLimit is the size of the executable (__TEXT) segment.
func codeSignature(content []byte, identifier string, codeLimit, execSegLimit int) []byte {
	id := append([]byte(identifier), 0)
	hashOffset := cdHeaderLen + len(id)
	nCodeSlots := (codeLimit + csPageSizeBytes - 1) / csPageSizeBytes
	cdLen := hashOffset + nCodeSlots*csHashSize

	cd := make([]byte, cdLen)
	be := binary.BigEndian
	be.PutUint32(cd[0:], csCodeDirMagic)
	be.PutUint32(cd[4:], uint32(cdLen))
	be.PutUint32(cd[8:], csVersion)
	be.PutUint32(cd[12:], csAdhoc)
	be.PutUint32(cd[16:], uint32(hashOffset))
	be.PutUint32(cd[20:], cdHeaderLen) // identOffset
	be.PutUint32(cd[24:], 0)           // nSpecialSlots
	be.PutUint32(cd[28:], uint32(nCodeSlots))
	be.PutUint32(cd[32:], uint32(codeLimit))
	cd[36] = csHashSize
	cd[37] = csHashTypeSHA256
	cd[38] = 0 // platform
	cd[39] = csLog2PageSize
	// cd[40:64] spare2/scatterOffset/teamOffset/spare3/codeLimit64 = 0
	be.PutUint64(cd[64:], 0)                    // execSegBase
	be.PutUint64(cd[72:], uint64(execSegLimit)) // execSegLimit
	be.PutUint64(cd[80:], csExecSegMainBinary)  // execSegFlags
	copy(cd[cdHeaderLen:], id)

	if content != nil {
		for i := 0; i < nCodeSlots; i++ {
			start := i * csPageSizeBytes
			end := start + csPageSizeBytes
			if end > len(content) {
				end = len(content)
			}
			h := sha256.Sum256(content[start:end])
			copy(cd[hashOffset+i*csHashSize:], h[:])
		}
	}

	// SuperBlob: header (12) + one BlobIndex (8) + CodeDirectory.
	const indexOff = 12 + 8
	total := indexOff + cdLen
	sb := make([]byte, total)
	be.PutUint32(sb[0:], csSuperBlobMagic)
	be.PutUint32(sb[4:], uint32(total))
	be.PutUint32(sb[8:], 1) // count
	be.PutUint32(sb[12:], csslotCodeDirectory)
	be.PutUint32(sb[16:], indexOff)
	copy(sb[indexOff:], cd)
	return sb
}

const csPageSizeBytes = 1 << csLog2PageSize
