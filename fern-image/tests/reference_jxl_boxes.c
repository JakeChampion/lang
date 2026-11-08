/* Offline reference only. Production image code has no libjxl dependency. */
#include <inttypes.h>
#include <jxl/decode.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static void emit(const JxlBoxType type, uint64_t size, const uint8_t *bytes,
                 size_t count) {
  for (size_t i = 0; i < 4; ++i) printf("%02x", (unsigned char)type[i]);
  printf(" %" PRIu64 " ", size);
  for (size_t i = 0; i < count; ++i) printf("%02x", bytes[i]);
  putchar('\n');
}

int main(int argc, char **argv) {
  if (argc != 2) return 1;
  if (JxlDecoderVersion() != 12000) return 9;
  FILE *file = fopen(argv[1], "rb");
  if (!file || fseek(file, 0, SEEK_END)) return 2;
  long length = ftell(file);
  if (length <= 0 || length > 1048576 || fseek(file, 0, SEEK_SET)) return 3;
  uint8_t *input = malloc((size_t)length);
  uint8_t *output = malloc((size_t)length);
  if (!input || !output) return 4;
  if (fread(input, 1, (size_t)length, file) != (size_t)length) return 5;
  fclose(file);
  JxlDecoder *decoder = JxlDecoderCreate(NULL);
  if (!decoder) return 6;
  if (JxlDecoderSubscribeEvents(decoder, JXL_DEC_BOX) != JXL_DEC_SUCCESS ||
      JxlDecoderSetInput(decoder, input, (size_t)length) != JXL_DEC_SUCCESS)
    return 7;
  JxlDecoderCloseInput(decoder);
  JxlBoxType type = {0};
  uint64_t size = 0;
  int active = 0;
  int result = 8;
  for (size_t event = 0; event <= (size_t)length / 8 + 2; ++event) {
    JxlDecoderStatus status = JxlDecoderProcessInput(decoder);
    if (status != JXL_DEC_BOX && status != JXL_DEC_SUCCESS) break;
    if (active) {
      size_t remaining = JxlDecoderReleaseBoxBuffer(decoder);
      if (remaining > (size_t)length) break;
      emit(type, size, output, (size_t)length - remaining);
    }
    if (status == JXL_DEC_SUCCESS) {
      result = 0;
      break;
    }
    if (JxlDecoderGetBoxType(decoder, type, JXL_FALSE) != JXL_DEC_SUCCESS ||
        JxlDecoderGetBoxSizeRaw(decoder, &size) != JXL_DEC_SUCCESS ||
        JxlDecoderSetBoxBuffer(decoder, output, (size_t)length) != JXL_DEC_SUCCESS)
      break;
    active = 1;
  }
  JxlDecoderDestroy(decoder);
  free(output);
  free(input);
  return result;
}
