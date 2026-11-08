#include <jxl/decode.h>
#include <jxl/encode.h>

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static float from_bits(const char* text) {
  const uint32_t bits = (uint32_t)strtoul(text, NULL, 10);
  float value;
  memcpy(&value, &bits, sizeof(value));
  return value;
}

static uint32_t to_bits(float value) {
  uint32_t bits;
  memcpy(&bits, &value, sizeof(bits));
  return bits;
}

int main(int argc, char** argv) {
  if (argc != 5 || JxlEncoderVersion() != 12000 || JxlDecoderVersion() != 12000) return 1;
  JxlEncoder* encoder = JxlEncoderCreate(NULL);
  if (!encoder) return 2;
  JxlBasicInfo info;
  JxlEncoderInitBasicInfo(&info);
  info.xsize = 1;
  info.ysize = 1;
  info.num_color_channels = 3;
  info.uses_original_profile = JXL_TRUE;
  info.intensity_target = from_bits(argv[1]);
  info.min_nits = from_bits(argv[2]);
  info.linear_below = from_bits(argv[3]);
  info.relative_to_max_display = JXL_FALSE;
  if (JxlEncoderUseContainer(encoder, JXL_FALSE) != JXL_ENC_SUCCESS ||
      JxlEncoderSetBasicInfo(encoder, &info) != JXL_ENC_SUCCESS) return 3;
  JxlColorEncoding color;
  JxlColorEncodingSetToSRGB(&color, JXL_FALSE);
  if (JxlEncoderSetColorEncoding(encoder, &color) != JXL_ENC_SUCCESS) return 4;
  JxlEncoderFrameSettings* settings = JxlEncoderFrameSettingsCreate(encoder, NULL);
  if (!settings || JxlEncoderSetFrameLossless(settings, JXL_TRUE) != JXL_ENC_SUCCESS ||
      JxlEncoderFrameSettingsSetOption(settings, JXL_ENC_FRAME_SETTING_EFFORT, 1) != JXL_ENC_SUCCESS) return 5;
  const JxlPixelFormat format = {3, JXL_TYPE_UINT8, JXL_NATIVE_ENDIAN, 0};
  const uint8_t samples[3] = {0, 128, 255};
  if (JxlEncoderAddImageFrame(settings, &format, samples, sizeof(samples)) != JXL_ENC_SUCCESS) return 6;
  JxlEncoderCloseInput(encoder);
  uint8_t output[65536];
  uint8_t* next = output;
  size_t available = sizeof(output);
  if (JxlEncoderProcessOutput(encoder, &next, &available) != JXL_ENC_SUCCESS) return 7;
  const size_t size = (size_t)(next - output);
  JxlEncoderDestroy(encoder);
  JxlDecoder* decoder = JxlDecoderCreate(NULL);
  if (!decoder || JxlDecoderSubscribeEvents(decoder, JXL_DEC_BASIC_INFO) != JXL_DEC_SUCCESS ||
      JxlDecoderSetInput(decoder, output, size) != JXL_DEC_SUCCESS) return 8;
  JxlDecoderCloseInput(decoder);
  if (JxlDecoderProcessInput(decoder) != JXL_DEC_BASIC_INFO ||
      JxlDecoderGetBasicInfo(decoder, &info) != JXL_DEC_SUCCESS) return 9;
  JxlDecoderDestroy(decoder);
  if (info.xsize != 1 || info.ysize != 1 || info.bits_per_sample != 8 || info.relative_to_max_display) return 10;
  FILE* file = fopen(argv[4], "wb");
  if (!file || fwrite(output, 1, size, file) != size || fclose(file)) return 11;
  printf("%u %u %u\n", to_bits(info.intensity_target), to_bits(info.min_nits), to_bits(info.linear_below));
  return 0;
}
