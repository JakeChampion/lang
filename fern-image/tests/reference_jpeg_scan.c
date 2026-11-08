#include <stdio.h>
#include <stdlib.h>
#include <jpeglib.h>

int main(int argc, char **argv) {
  if (argc != 2) return 2;
  FILE *input = fopen(argv[1], "rb");
  if (!input) return 3;
  struct jpeg_decompress_struct decoder;
  struct jpeg_error_mgr errors;
  decoder.err = jpeg_std_error(&errors);
  jpeg_create_decompress(&decoder);
  jpeg_stdio_src(&decoder, input);
  if (jpeg_read_header(&decoder, TRUE) != JPEG_HEADER_OK) return 4;
  jvirt_barray_ptr *arrays = jpeg_read_coefficients(&decoder);
  printf("frame %u %u %d\n", decoder.image_width, decoder.image_height, decoder.num_components);
  for (int c = 0; c < decoder.num_components; c++) {
    jpeg_component_info *component = &decoder.comp_info[c];
    printf("component %d %u %u\n", c, component->width_in_blocks, component->height_in_blocks);
    for (JDIMENSION y = 0; y < component->height_in_blocks; y++) {
      JBLOCKARRAY row = (*decoder.mem->access_virt_barray)((j_common_ptr)&decoder, arrays[c], y, 1, FALSE);
      for (JDIMENSION x = 0; x < component->width_in_blocks; x++) {
        printf("block %d %u %u", c, x, y);
        for (int k = 0; k < 64; k++) printf(" %d", (int)row[0][x][k]);
        putchar('\n');
      }
    }
  }
  if (!jpeg_finish_decompress(&decoder)) return 5;
  jpeg_destroy_decompress(&decoder);
  fclose(input);
  return 0;
}
