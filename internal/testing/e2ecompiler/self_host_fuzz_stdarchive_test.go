package e2ecompiler

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// The targets here feed hostile bytes to the archive decoders of the standard
// library through tests/fuzz/stdparse.fern: bzip2, LZ4, the LZW of compress,
// xz, lzma and ZIP. A tool that unpacks a file from a stranger reads these
// bytes. Each decoder runs under the output limit of the driver, so a small
// input cannot ask for more than that. The bzip2 target compares each stream
// that Fern accepts with Go's compress/bzip2. Go has no decoder of the other
// formats.

// The streams of each format, written by the reference tools, as hex.
var bzip2SeedHex = []string{
	"425a6839314159265359cc2e03a10000065180001040040644908020002190320400c2a832d9416e9c079af177245385090cc2e03a10",
	"425a6831314159265359e423f06600004a81003c00200030cd340a547a62918523e415882b85dc914e14243908fc1980",
	"425a6839314159265359d1c7428100002f7ffff72972d7646385a458dc77df06594023d89ecfd5f12b5bfd7481ef8329e777ecb000b26388c9820311886806134c988c21a1a0681804346690c4326268c993350c68ca63d4f15100c864f4468c8c134c8d1a6d103003443d083d4311b4043d4f519308c1a4c9b29e8d11ea34c99106989804c0130469a60004c009a1a189800000004611804698990c0d787933d9f1d281d9cd2e9a89915e16e0068f5bcdf18411d15701645a7ec6061885c10df399a315cfb5488e20abe7fe93221677419f158da04123822183d0aa0883d51a5ec49126413fd7e068c6fdd92bb7ba05f8bfc0affba96ec800abef75f9661674b9a9e844a255f30251f4c2a037fd7d4576a9f402fbc4ddb84dd38b77ba041b422c4702473d5e0308cab3f32ad8ff81700482fdef53db0685aa47b8f6c34b8560d94af61c70f0177245385090d1c74281",
	"425a68393141592653599e22956700004e4002c00000400008200030cc0529a62a3625478bb9229c28484f114ab380",
	"425a683917724538509000000000",
	"425a6839314159265359cc2e03a10000065180001040040644908020002190320400c2a832d9416e9c079af177245385090cc2e03a10425a6839314159265359e423f06600004a81003c00200030cd340a547a62918523e415882b85dc914e14243908fc1980",
	"425a6839314159265359cc2e03a10000065180001040040644908020002190320400c2a832d9416e9c079af177245385090cc2e03a10747261696c696e67",
	// Two symbols of a coding table have the same code. The bzip2 tool
	// decodes the stream, and compress/bzip2 refuses the table.
	"425a68393141592653599e22956700004e4002c00000400008200030b00529a62a3625478bb9229c28484f114ab380",
}

var xzSeedHex = []string{
	"fd377a585a0000016922de360200210116000000742fe5a3e0001900145d00341949ee8def8c873201b0b9beaf99b9fd20000000875d462b00012c1aff27d5e49042990d010000000001595a",
	"fd377a585a000004e6d6b4460200210116000000742fe5a3e00a8b001a5d00309888a5836e0e0160a3361aca336c111dd0a5fd2329ad4d6c00000000471e3e608398e81a0001368c1500000053caa702b1c467fb020000000004595a",
	"fd377a585a00000ae1fb0ca10200210116000000742fe5a30100c782b70eee7f1a5039bef07ec2347f066ed08f5dc7512447e3404300026b6e545594a065685d64c4980bb8d4544a8721a99a01ad219eb59cf6a15ef6f15a1d830bb7ce09d6bbc004e7175c643c7decb0b580ec37bc9712dd2e6aaeb94bae8d2f9fa29c5a284c9ef7521829cf1079b080e9d74a1c10fcab6a4243d33656debe4c1ed79648e856e8f9a2f58c95f0ce4b39c15bffad5c2dfb8bb820b6119cba8ff88796ae5b05f280a68ced93b6b28cb0d1b358e6baab485565b9f49028d557d79a8a0e6451e15c705c15001641661a33859edd817b62a80c690313a7610ccf0babf931a1bd79a683075cec0001f801c8010000c7cacfb5b6e9df1c02000000000a595a",
	"fd377a585a000000ff12d9410200210116000000742fe5a3e09c3f00455d00006ffdffffa3b7ff473e481572396151b89228e6a38607f9eee41e82d32fc53a3c014bb17ec98a8a4d2fa30dd97fa6e38c231153e05918c5758ae277f8b6947a631d4e000000000000000159c0b802000022d783daa8000afc020000000000595a",
	"fd377a585a000004e6d6b446000000001cdf44211fb6f37d010000000004595a",
	"fd377a585a000004e6d6b4460200210116000000742fe5a3e0001900145d00341949ee8def8c873201b0b9beaf99b9fd2000000068294b5248ce641d0001301aa27aa2021fb6f37d010000000004595afd377a585a000004e6d6b4460200210116000000742fe5a3e00a8b001a5d00309888a5836e0e0160a3361aca336c111dd0a5fd2329ad4d6c00000000471e3e608398e81a0001368c1500000053caa702b1c467fb020000000004595a00000000",
}

// The lzma seeds give the size as unknown, as its true value, and as values
// that 32 bits do not hold.
var lzmaSeedHex = []string{
	"5d00008000ffffffffffffffff00341949ee8def8c873201b0b9beaf99ba522da3ffffda5e0000",
	"5d00008000ffffffffffffffff00309888a5836e0e0160a3361aca336c111dd0a5fd2329ba2a3e7cfffa808000",
	"5d000080008c0a00000000000000309888a5836e0e0160a3361aca336c111dd0a5fd2329ba2a3e7cfffa808000",
	"5d00008000050000000100000000309888a5836e0e0160a3361aca336c111dd0a5fd2329ba2a3e7cfffa808000",
	"5d00008000000000000000008000309888a5836e0e0160a3361aca336c111dd0a5fd2329ba2a3e7cfffa808000",
	"5d00008000ffffff7f0000000000309888a5836e0e0160a3361aca336c111dd0a5fd2329ba2a3e7cfffa808000",
	"5d00008000ffffffffffffffff00006ffdffffa3b7ff473e481572396151b89228e6a38607f9eee41e82d32fc53a3c014bb17ec98a8a4d2fa30dd97fa6e38c231153e05918c5758ae277f8b6947a632383d873ffff85f80000",
}

// The lz4 seeds have a frame, a legacy frame, a skippable frame and a content
// size, and block sizes and counts near the limits of 32 bits.
var lz4SeedHex = []string{
	"04224d18604055120000007868656c6c6f2c2007007020776f726c640a00000000",
	"04224d1860405521000000316162630300116406003f6361620900ffffffffffffffffffff6d50626361626400000000",
	"02214c1821000000316162630300116406003f6361620900ffffffffffffffffffff6d506263616264",
	"04224d186840409c00000000000055a70000001f000100ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffc350000000000000000000532a4d1805000000000000000004224d18604055120000007868656c6c6f2c2007007020776f726c640a00000000",
	"04224d18604055c800008082b70eee7f1a5039bef07ec2347f066ed08f5dc7512447e3404300026b6e545594a065685d64c4980bb8d4544a8721a99a01ad219eb59cf6a15ef6f15a1d830bb7ce09d6bbc004e7175c643c7decb0b580ec37bc9712dd2e6aaeb94bae8d2f9fa29c5a284c9ef7521829cf1079b080e9d74a1c10fcab6a4243d33656debe4c1ed79648e856e8f9a2f58c95f0ce4b39c15bffad5c2dfb8bb820b6119cba8ff88796ae5b05f280a68ced93b6b28cb0d1b358e6baab485565b9f49028d557d79a8a0e6451e15c705c1500000000",
	"04224d18604055ffffff7f316162630300116406003f6361620900ffffffffffffffffffff6d50626361626400000000",
	"04224d18604055ffffffff316162630300116406003f6361620900ffffffffffffffffffff6d50626361626400000000",
	"02214c18f0ffffffffffffffffffffffffffffffffffffffffffffff",
	"04224d1860408206000000ff6162ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
}

var lzwSeedHex = []string{
	"1f9d9068cab061f386058880030b1e144810c49d3772d8905100",
	"1f9d9061c48c0938500c1982080d262c785060c28605192e0c08f1e144850e3356d47871a3c48c1f2352ec4812e448931845a6b488d2e349952e57726c5912664d963669eaccc913a7cf993d81fe0c39f465519944852a4dcad4e852a74d91429d2ab56accab379f5acd1a15ebcea35e8376e54a352c58b25bbf6a35bb162ddbb16ae18a2debb66e5cba77d3ced57b36ef5bbc7bfff26debd76e60c37d0f175eacb871e2c7841d47862c973260cb832b4fdeacb9f365ce9f3d670e4d7ab469c1a811833ead5a746ac6985f4b76ddbab4ecd8b559c35e7d9b77eedeb477079f6dfbb771e1c591eb26be1c774b",
	"1f9d8c61c48c0938500c1982080d262c785060c28605192e0c08f1e144850e3356d47871a3c48c1f2352ec4812e448931845a6b488d2e349952e57726c5912664d963669eaccc913a7cf993d81fe0c39f465519944852a4dcad4e852a74d91429d2ab56accab379f5acd1a15ebcea35e8376e54a352c58b25bbf6a35bb162ddbb16ae18a2debb66e5cba77d3ced57b36ef5bbc7bfff26debd76e60c37d0f175eacb871e2c7841d47862c973260cb832b4fdeacb9f365ce9f3d670e4d7ab469c1a811833ead5a746ac6985f4b76ddbab4ecd8b559c35e7d9b77eedeb477079f6dfbb771e1c591eb26be1c774b",
	"1f9d8a00020a1c48b0a0c18308132a5cc8b0a1c38710234a9c48b1a2c58b18336adcc8b1a3c78f20438a1c49b2a4c9932853aa5cc9b2a5cb973063ca9c49b3a6cd9b3873eadcc9b3a7cf9f40830a1d4ab4a8d1a348932a5dcab4a9d3a750a34a9d4ab5aad5ab58b36addcab5abd7af60c38a1d4bb6acd9b368d3aa5dcbb6addbb770e3ca9d4bb7aeddbb78f3eaddcbb7afdfbf80030b1e4cb8b0e1c388132b5eccb8b1e3c790234b9e4cb9b2e5cb98336bdeccb9b3e7cfa0438b1e4dbab4e9d3a853ab5ecdbab5ebd7b063cb9e4dbbb6eddbb873ebdecdbbb7efdfc0830b1f4ebcb8f1e3c8932b5fcebcb9f3e7d0a34b9f4ebdbaf5ebd8b36bdfcebdbbf7efe0c38b1f4fbebcf9f3e8d3ab5fcfbebdfbf7f0e3cb9f4fbfbefdfbf8f3ebdfcfbfbfffff000628e080041668e0810826a8e0820c36e8e083104628e184145668e18518660816",
	"1f9d90826e3970f74703941cbee0f91146e38f0137d01e75391685c4917140860010b0c60d952a94409541d3850c314c0b7051a3a2e45088549a02b40ae1a916277ba1bcd88ba7a5c3a005b79c25b0b60b18817317b890e1d1871dac5a80d8dde075494237176a5ce55ae2aad18b4fa2386941c1c4d33d2918523c83900716a074d7947080c06f951a2143a6d9b0e2cd17130fd72c214167055d3e51f5185582e76c498e605bfeb5e2d262df225c206c45e0a4eb11be43965c6d29200f902946ed26d992c50856b45958cce95a85a44a995cf420a1a876e59a26450ec84409c7050e970a",
	"1f9d9061028601",
	"1f9d90000102000102",
}

// streamSeeds gives each stream of hexes, and the damaged forms of each.
func streamSeeds(hexes []string) func(testing.TB) [][]byte {
	return func(f testing.TB) [][]byte {
		seeds := [][]byte{{}}
		for _, h := range hexes {
			stream := hexSeed(f, h)
			seeds = append(seeds, stream)
			seeds = append(seeds, damaged(stream)...)
		}
		return seeds
	}
}

// bzip2Seeds adds the first stream with each bit of its first table bytes
// flipped. One of them has a selector that names the table after the last
// one, which the decoder read.
func bzip2Seeds(f testing.TB) [][]byte {
	seeds := streamSeeds(bzip2SeedHex)(f)
	stream := hexSeed(f, bzip2SeedHex[0])
	for at := 14; at < len(stream) && at < 40; at++ {
		for bit := 0; bit < 8; bit++ {
			flipped := append([]byte(nil), stream...)
			flipped[at] ^= 1 << bit
			seeds = append(seeds, flipped)
		}
	}
	return seeds
}

// goBunzip2 decodes input with compress/bzip2. It gives the bytes before
// an error too, which a caller compares with the bytes that Fern accepts.
func goBunzip2(input []byte) (out []byte, err error) {
	out, err = io.ReadAll(io.LimitReader(bzip2.NewReader(bytes.NewReader(input)), stdDecodeLimit+1))
	return out, err
}

// toolBunzip2 decodes input with the bzip2 tool, which std/bzip2 follows.
func toolBunzip2(input []byte) (out []byte, err error) {
	tool, err := exec.LookPath("bzip2")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(tool, "-dc")
	cmd.Stdin = bytes.NewReader(input)
	return cmd.Output()
}

// bzip2AgreesWithGo compares a summary with compress/bzip2. What Fern
// accepts, it must decode to the bytes that Go read. Go refuses bytes after
// the end of a stream, which the bzip2 tool and Fern ignore, so the bytes
// that Go read before its error count. Go also refuses a coding table that
// the bzip2 tool accepts, so the tool decides a stream that Go refuses and
// Fern decodes to other bytes. Go accepts some streams that the bzip2 tool
// refuses, so a refusal by Fern is not compared.
func bzip2AgreesWithGo(r readerRun) string {
	out, err := goBunzip2(r.input)
	if len(out) > stdDecodeLimit {
		return ""
	}
	fields := strings.Fields(r.summary)
	want := fmt.Sprintf("%d crc %d", len(out), crc32.ChecksumIEEE(out))
	switch {
	case len(fields) == 5 && fields[1] == "ok":
		got := strings.Join(fields[2:], " ")
		if got == want {
			return ""
		}
		if err != nil {
			tool, toolErr := toolBunzip2(r.input)
			if toolErr == nil && got == fmt.Sprintf("%d crc %d", len(tool), crc32.ChecksumIEEE(tool)) {
				return ""
			}
			return fmt.Sprintf("bzip2 on %d bytes gives %q, Go gives %s (error %v), and the bzip2 tool gives %d bytes (error %v)\ninput: %x", len(r.input), r.summary, want, err, len(tool), toolErr, r.input)
		}
		return fmt.Sprintf("bzip2 on %d bytes gives %q, Go gives %s\ninput: %x", len(r.input), r.summary, want, r.input)
	case len(fields) == 4 && fields[1] == "refused":
	default:
		return fmt.Sprintf("bzip2 gives %q, which is not a summary", r.summary)
	}
	return ""
}

// zipSeeds gives archives that Go writes: stored and compressed files, a
// directory, an empty archive and a comment, and the damaged forms of each.
// Three more have a size, an offset and a count that the bytes do not hold.
func zipSeeds(f testing.TB) [][]byte {
	write := func(method uint16, comment string, files ...string) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, name := range files {
			fw, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
			if err != nil {
				f.Fatal(err)
			}
			if !strings.HasSuffix(name, "/") {
				fw.Write([]byte(strings.Repeat(name+" body\n", 40)))
			}
		}
		w.SetComment(comment)
		if err := w.Close(); err != nil {
			f.Fatal(err)
		}
		return b.Bytes()
	}
	archives := [][]byte{
		write(zip.Store, "", "a.txt"),
		write(zip.Deflate, "", "a.txt", "d/", "d/b.txt"),
		write(zip.Deflate, "PK\x05\x06 is not a record here", "a.txt"),
		write(zip.Store, ""),
	}
	seeds := [][]byte{{}}
	for _, a := range archives {
		seeds = append(seeds, a)
		seeds = append(seeds, damaged(a)...)
	}
	// The record at the end is the last 22 bytes of an archive with no comment.
	base := archives[1]
	end := len(base) - 22
	for _, edit := range []struct {
		at    int
		value uint32
	}{{end + 16, 0x7fffffff}, {end + 16, 0xffffffff}, {end + 12, 0x7fffffff}} {
		moved := append([]byte(nil), base...)
		binary.LittleEndian.PutUint32(moved[edit.at:], edit.value)
		seeds = append(seeds, moved)
	}
	counted := append([]byte(nil), base...)
	binary.LittleEndian.PutUint16(counted[end+8:], 0xfffe)
	binary.LittleEndian.PutUint16(counted[end+10:], 0xfffe)
	seeds = append(seeds, counted)
	// The first record of the central directory: its sizes and the offset of
	// its local header.
	central := int(binary.LittleEndian.Uint32(base[end+16:]))
	for _, at := range []int{central + 20, central + 24, central + 42} {
		for _, value := range []uint32{0x7fffffff, 0x80000000, 0xffffffff, 1 << 21} {
			moved := append([]byte(nil), base...)
			binary.LittleEndian.PutUint32(moved[at:], value)
			seeds = append(seeds, moved)
		}
	}
	return seeds
}

// FuzzSelfHostStdBzip2 feeds hostile bytes to the bzip2 decoder, and compares
// each input with Go's compress/bzip2.
func FuzzSelfHostStdBzip2(f *testing.F) {
	fuzzReaderTarget(f)
}

// FuzzSelfHostStdLz4 feeds hostile bytes to the LZ4 frame decoder.
func FuzzSelfHostStdLz4(f *testing.F) {
	fuzzReaderTarget(f)
}

// FuzzSelfHostStdLzw feeds hostile bytes to the decoder of the `.Z` files of
// compress.
func FuzzSelfHostStdLzw(f *testing.F) {
	fuzzReaderTarget(f)
}

// FuzzSelfHostStdXz feeds hostile bytes to the xz decoder.
func FuzzSelfHostStdXz(f *testing.F) {
	fuzzReaderTarget(f)
}

// FuzzSelfHostStdLzma feeds hostile bytes to the decoder of `.lzma` files.
func FuzzSelfHostStdLzma(f *testing.F) {
	fuzzReaderTarget(f)
}

// FuzzSelfHostStdZip feeds hostile bytes to the ZIP reader, and reads each
// entry of an archive that it accepts.
func FuzzSelfHostStdZip(f *testing.F) {
	fuzzReaderTarget(f)
}

var archiveTargets = []string{"FuzzSelfHostStdBzip2", "FuzzSelfHostStdLz4", "FuzzSelfHostStdLzw", "FuzzSelfHostStdXz", "FuzzSelfHostStdLzma", "FuzzSelfHostStdZip"}

// TestSelfHostStdArchiveReaderSeeds runs each seed of the six readers in a
// plain test run. A seed must give a summary with exit 0, the reader must
// accept one seed at least, and it must refuse one.
func TestSelfHostStdArchiveReaderSeeds(t *testing.T) {
	bin := fuzzStdParse(t)
	for _, target := range archiveTargets {
		r := readerTargetOf(t, target)
		t.Run(r.reader, func(t *testing.T) {
			accepted, refused := 0, 0
			for i, in := range r.seeds(t) {
				summary := runStdReader(t, bin, r.reader, in)
				switch {
				case strings.HasPrefix(summary, r.reader+" ok"):
					accepted++
				case strings.HasPrefix(summary, r.reader+" refused"):
					refused++
				default:
					t.Errorf("seed %d: the summary is %q", i, summary)
				}
				if r.check != nil {
					if fault := r.check(readerRun{reader: r.reader, input: in, summary: summary}); fault != "" {
						t.Errorf("seed %d: %s", i, fault)
					}
				}
			}
			if accepted == 0 || refused == 0 {
				t.Errorf("the reader accepts %d seeds and refuses %d, want one of each at least", accepted, refused)
			}
		})
	}
}

// TestSelfHostStdArchiveLimits pins the output limit of each decoder: a
// stream of 40000 zero bytes decodes, and the same stream is refused with
// its first bytes under a limit of 100.
func TestSelfHostStdArchiveLimits(t *testing.T) {
	want := fmt.Sprintf("ok 40000 crc %d", crc32.ChecksumIEEE(make([]byte, 40000)))
	bin := fuzzStdParse(t)
	for reader, h := range map[string]string{"bzip2": bzip2SeedHex[3], "xz": xzSeedHex[3], "lzma": lzmaSeedHex[6], "lz4": lz4SeedHex[3][:380], "lzw": lzwSeedHex[3]} {
		in := hexSeed(t, h)
		if got := runStdReader(t, bin, reader, in); got != reader+" "+want {
			t.Errorf("%s gives %q, want %q", reader, got, reader+" "+want)
		}
		if got, limited := runStdReader(t, bin, reader+"-limit", in), reader+"-limit refused partial 100"; got != limited {
			t.Errorf("%s under a limit of 100 gives %q, want %q", reader, got, limited)
		}
	}
}
