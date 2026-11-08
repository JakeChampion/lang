/* Reference harness for libvorbis's public float synthesis API.
 * Build with cc -O2 -I INCLUDE vorbis-packets.c -lvorbis -o vorbis-packets.
 * Input is a sequence of little-endian u32 lengths followed by packet bytes.
 * The first three packets are headers. Audio packets have no container timestamps.
 * Output has one u32 frame count followed by interleaved float PCM per audio packet.
 * This harness is reference-only and is not linked into Fern.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <vorbis/codec.h>

static int read_u32(uint32_t *value) {
  unsigned char bytes[4];
  size_t count = fread(bytes, 1, 4, stdin);
  if (!count && feof(stdin)) return 0;
  if (count != 4) exit(2);
  *value = bytes[0] | (uint32_t)bytes[1] << 8 |
    (uint32_t)bytes[2] << 16 | (uint32_t)bytes[3] << 24;
  return 1;
}

static void write_u32(uint32_t value) {
  unsigned char bytes[4] = {value, value >> 8, value >> 16, value >> 24};
  if (fwrite(bytes, 1, 4, stdout) != 4) exit(3);
}

int main(int argc, char **argv) {
  if (argc == 2 && !strcmp(argv[1], "--version")) {
    puts(vorbis_version_string());
    return 0;
  }
  static const int mapping[9][8] = {
    {0}, {0}, {0,1}, {0,2,1}, {0,1,2,3}, {0,2,1,3,4},
    {0,2,1,5,3,4}, {0,2,1,6,5,3,4}, {0,2,1,7,5,6,3,4}
  }; /* Output WAVE channel -> input Vorbis channel. */
  vorbis_info info;
  vorbis_comment comments;
  vorbis_dsp_state dsp;
  vorbis_block block;
  vorbis_info_init(&info);
  vorbis_comment_init(&comments);
  uint32_t size;
  int64_t packetno = 0;
  while (read_u32(&size)) {
    if (!size || size > 4194304) return 4;
    unsigned char *bytes = malloc(size);
    if (!bytes || fread(bytes, 1, size, stdin) != size) return 5;
    ogg_packet packet = {0};
    packet.packet = bytes;
    packet.bytes = size;
    packet.packetno = packetno;
    packet.b_o_s = packetno == 0;
    packet.granulepos = -1;
    if (packetno < 3) {
      if (vorbis_synthesis_headerin(&info, &comments, &packet)) return 6;
      if (packetno == 2) {
        if (info.channels < 1 || info.channels > 8 || vorbis_synthesis_init(&dsp, &info) || vorbis_block_init(&dsp, &block)) return 7;
      }
    } else {
      if (vorbis_synthesis(&block, &packet) || vorbis_synthesis_blockin(&dsp, &block)) return 8;
      float **pcm;
      int frames = vorbis_synthesis_pcmout(&dsp, &pcm);
      if (frames < 0) return 9;
      write_u32(frames);
      for (int i = 0; i < frames; ++i) {
        for (int channel = 0; channel < info.channels; ++channel) {
          float sample = pcm[mapping[info.channels][channel]][i];
          uint32_t bits;
          memcpy(&bits, &sample, 4);
          write_u32(bits);
        }
      }
      if (vorbis_synthesis_read(&dsp, frames)) return 10;
    }
    free(bytes);
    ++packetno;
  }
  if (packetno < 3) return 11;
  vorbis_block_clear(&block);
  vorbis_dsp_clear(&dsp);
  vorbis_comment_clear(&comments);
  vorbis_info_clear(&info);
  return ferror(stdout) ? 12 : 0;
}
