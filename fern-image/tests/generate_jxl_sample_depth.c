#include <jxl/decode.h>
#include <jxl/encode.h>

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char** argv) {
  if (argc != 4 || JxlEncoderVersion() != 12000 || JxlDecoderVersion() != 12000) return 1;
  const uint32_t bits = (uint32_t)strtoul(argv[1], NULL, 10);
  const uint32_t exponent = (uint32_t)strtoul(argv[2], NULL, 10);
  JxlEncoder* encoder = JxlEncoderCreate(NULL);
  if (!encoder) return 2;
  JxlBasicInfo info;
  JxlEncoderInitBasicInfo(&info);
  info.xsize = 1;
  info.ysize = 1;
  info.bits_per_sample = bits;
  info.exponent_bits_per_sample = exponent;
  info.num_color_channels = 3;
  info.uses_original_profile = JXL_TRUE;
  if (JxlEncoderUseContainer(encoder, JXL_FALSE) != JXL_ENC_SUCCESS ||
      JxlEncoderSetBasicInfo(encoder, &info) != JXL_ENC_SUCCESS) return 3;
  JxlColorEncoding color;
  JxlColorEncodingSetToSRGB(&color, JXL_FALSE);
  if (JxlEncoderSetColorEncoding(encoder, &color) != JXL_ENC_SUCCESS) return 4;
  JxlEncoderFrameSettings* settings = JxlEncoderFrameSettingsCreate(encoder, NULL);
  if (!settings || JxlEncoderSetFrameLossless(settings, JXL_TRUE) != JXL_ENC_SUCCESS ||
      JxlEncoderFrameSettingsSetOption(settings, JXL_ENC_FRAME_SETTING_EFFORT, 1) != JXL_ENC_SUCCESS) return 5;
  const JxlPixelFormat format = {3, exponent ? JXL_TYPE_FLOAT : JXL_TYPE_UINT16, JXL_NATIVE_ENDIAN, 0};
  const uint16_t integer_samples[3] = {0, 32768, 65535};
  const float float_samples[3] = {0.25f, 0.5f, 0.75f};
  const void* samples = exponent ? (const void*)float_samples : (const void*)integer_samples;
  const size_t sample_bytes = exponent ? sizeof(float_samples) : sizeof(integer_samples);
  if (JxlEncoderAddImageFrame(settings, &format, samples, sample_bytes) != JXL_ENC_SUCCESS) return 6;
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
  if (info.bits_per_sample != bits || info.exponent_bits_per_sample != exponent) return 10;
  FILE* file = fopen(argv[3], "wb");
  if (!file || fwrite(output, 1, size, file) != size || fclose(file)) return 11;
  printf("%u %u %zu\n", bits, exponent, size);
  return 0;
}
