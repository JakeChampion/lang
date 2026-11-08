/* Reference-only harness for libvorbisfile's public float API.
 * Build with the public headers and libvorbisfile. This is not a Fern dependency.
 * Output records contain LE u32 link, rate, channels, frames, then float PCM.
 */
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <vorbis/vorbisfile.h>

static void word(uint32_t value) {
  unsigned char bytes[4] = {value, value >> 8, value >> 16, value >> 24};
  if (fwrite(bytes, 1, 4, stdout) != 4) { perror("write"); }
}

int main(int argc, char **argv) {
  if (argc != 3) return 2;
  FILE *file = fopen(argv[2], "rb");
  if (!file) return 3;
  OggVorbis_File state;
  ov_callbacks callbacks = !strcmp(argv[1], "stream") ? OV_CALLBACKS_STREAMONLY : OV_CALLBACKS_DEFAULT;
  if (ov_open_callbacks(file, &state, NULL, 0, callbacks)) { fclose(file); return 4; }
  static const int order[9][8] = {
    {0}, {0}, {0,1}, {0,2,1}, {0,1,2,3}, {0,2,1,3,4},
    {0,2,1,5,3,4}, {0,2,1,6,5,3,4}, {0,2,1,7,5,6,3,4}
  };
  while (1) {
    float **pcm;
    int link;
    long frames = ov_read_float(&state, &pcm, 4096, &link);
    if (frames < 0) { ov_clear(&state); return 5; }
    if (!frames) break;
    vorbis_info *info = ov_info(&state, -1);
    if (!info || info->channels < 1 || info->channels > 8) { ov_clear(&state); return 6; }
    word(link); word(info->rate); word(info->channels); word(frames);
    for (long i = 0; i < frames; ++i) {
      for (int ch = 0; ch < info->channels; ++ch) {
        uint32_t value;
        memcpy(&value, &pcm[order[info->channels][ch]][i], 4);
        word(value);
      }
    }
  }
  ov_clear(&state);
  return ferror(stdout) ? 7 : 0;
}
