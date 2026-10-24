package e2ecompiler

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostPNGFilters(t *testing.T) {
	const source = `import "std/png";
import "std/binary";
import "std/deflate";
function main(): i32 {
  for width in [1, 2, 7, 32] {
    let pixels: u8[] = __alloc_u8(width * 5 * 3);
    let at: i32 = 0;
    while (at < pixels.len()) {
      pixels = pixels.with(at, ((at * 73 + at / 7 * 31) & 255) as u8);
      at = at + 1;
    }
    for filter in [png.Filter.Unfiltered, png.Filter.Sub, png.Filter.Up, png.Filter.Average, png.Filter.Paeth, png.Filter.Adaptive] {
      for compression in [png.Compression.Balanced, png.Compression.Fast] {
        let opts: png.Options = png.Options { filter: filter, compression: compression };
        match (png.encode_rgb8_with_options(width, 5, pixels, opts)) {
          Ok(image) => {
            let frame: u8[] = binary.append_bytes(binary.append_u32_be([], image.len() as u32), image);
            match (stdout().write_bytes(frame)) { Ok(_) => {}, Err(_) => { return 1; }, }
          },
          Err(_) => { return 2; },
        }
      }
    }
  }
  for length in [0, 1, 2, 3, 258, 32768, 65536, 131072] {
    let raw: u8[] = __alloc_u8(length);
    let at: i32 = 0;
    let seed: i64 = 12345;
    while (at < length) {
      seed = seed * 48271 % 2147483647;
      let value: u8 = (seed & 255) as u8;
      if (at >= 32768) { value = raw[at - 32768]; }
      raw = raw.with(at, value);
      at = at + 1;
    }
    let compressed: u8[] = deflate.deflate_fast(raw);
    let frame: u8[] = binary.append_bytes(binary.append_u32_be([], compressed.len() as u32), compressed);
    match (stdout().write_bytes(frame)) { Ok(_) => {}, Err(_) => { return 3; }, }
  }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			src := writeFractalProgram(t, source)
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"))
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, cli.arm64Binary(t, src, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"))
			case "wasm32-wasi":
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", cli.emit(t, src, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"))
			}
			var errors bytes.Buffer
			cmd.Stderr = &errors
			stream, err := cmd.Output()
			if err != nil {
				t.Fatalf("PNG encoder: %v\n%s", err, errors.String())
			}
			assertBalancedCensus(t, errors.String())
			for _, width := range []int{1, 2, 7, 32} {
				for pair := 0; pair < 12; pair++ {
					if len(stream) < 4 {
						t.Fatal("missing PNG length")
					}
					n := int(binary.BigEndian.Uint32(stream[:4]))
					stream = stream[4:]
					if n > len(stream) {
						t.Fatal("truncated PNG frame")
					}
					image, err := png.Decode(bytes.NewReader(stream[:n]))
					if err != nil {
						t.Fatalf("width %d filter pair %d: %v", width, pair, err)
					}
					stream = stream[n:]
					if image.Bounds().Dx() != width || image.Bounds().Dy() != 5 {
						t.Fatal("PNG dimensions differ")
					}
					for y := 0; y < 5; y++ {
						for x := 0; x < width; x++ {
							r, g, b, a := image.At(x, y).RGBA()
							for channel, got := range []uint32{r, g, b} {
								at := (y*width+x)*3 + channel
								want := uint32((at*73+at/7*31)&255) * 257
								if got != want || a != 65535 {
									t.Fatalf("width %d pair %d pixel %d,%d channel %d = %d, want %d", width, pair, x, y, channel, got, want)
								}
							}
						}
					}
				}
			}
			for _, length := range []int{0, 1, 2, 3, 258, 32768, 65536, 131072} {
				if len(stream) < 4 {
					t.Fatal("missing raw DEFLATE length")
				}
				n := int(binary.BigEndian.Uint32(stream[:4]))
				stream = stream[4:]
				if n > len(stream) {
					t.Fatal("truncated raw DEFLATE frame")
				}
				decoder := flate.NewReader(bytes.NewReader(stream[:n]))
				raw, err := io.ReadAll(decoder)
				decoder.Close()
				if err != nil || len(raw) != length {
					t.Fatalf("DEFLATE length %d: got %d bytes, %v", length, len(raw), err)
				}
				seed := int64(12345)
				want := make([]byte, length)
				for at := range want {
					seed = seed * 48271 % 2147483647
					want[at] = byte(seed & 255)
					if at >= 32768 {
						want[at] = want[at-32768]
					}
				}
				if !bytes.Equal(raw, want) {
					t.Fatalf("DEFLATE bytes differ at input length %d", length)
				}
				stream = stream[n:]
			}
			if len(stream) != 0 {
				t.Fatal("unexpected data after the last frame")
			}
		})
	}
}

func writeFractalProgram(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
