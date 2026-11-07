#include <png.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Offline reference only. Emit exact RGBA8 without a color transform. */
int main(int argc, char **argv) {
  if (argc == 2 && strcmp(argv[1], "--version") == 0) {
    puts(png_get_libpng_ver(NULL));
    return 0;
  }
  if (argc != 2) return 2;
  FILE *input = fopen(argv[1], "rb");
  if (input == NULL) return 3;
  png_structp png = png_create_read_struct(PNG_LIBPNG_VER_STRING, NULL, NULL, NULL);
  if (png == NULL) return 4;
  png_infop info = png_create_info_struct(png);
  if (info == NULL) return 5;
  if (setjmp(png_jmpbuf(png))) return 6;
  png_init_io(png, input);
  png_set_user_limits(png, 65536, 65536);
  png_read_info(png, info);
  int depth = png_get_bit_depth(png, info);
  int color = png_get_color_type(png, info);
  if (depth > 8) return 7;
  if (color == PNG_COLOR_TYPE_PALETTE) png_set_palette_to_rgb(png);
  if (color == PNG_COLOR_TYPE_GRAY && depth < 8) png_set_expand_gray_1_2_4_to_8(png);
  if (png_get_valid(png, info, PNG_INFO_tRNS)) png_set_tRNS_to_alpha(png);
  else if (!(color & PNG_COLOR_MASK_ALPHA)) png_set_add_alpha(png, 255, PNG_FILLER_AFTER);
  if (!(color & PNG_COLOR_MASK_COLOR)) png_set_gray_to_rgb(png);
  png_read_update_info(png, info);
  png_uint_32 width = png_get_image_width(png, info);
  png_uint_32 height = png_get_image_height(png, info);
  size_t stride = png_get_rowbytes(png, info);
  if (stride != (size_t)width * 4 || (size_t)height * stride > 67108864) return 8;
  unsigned char *row = malloc(stride);
  if (row == NULL) return 9;
  for (png_uint_32 y = 0; y < height; ++y) {
    png_read_row(png, row, NULL);
    if (fwrite(row, 1, stride, stdout) != stride) return 10;
  }
  png_read_end(png, info);
  free(row);
  png_destroy_read_struct(&png, &info, NULL);
  return fclose(input) == 0 ? 0 : 11;
}
