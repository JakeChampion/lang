/* Offline RGB observations. No Fern runtime dependency. */
#include <stdio.h>
#include <stdlib.h>
#include <setjmp.h>
#include <jpeglib.h>

#define TEXT_INNER(x) #x
#define TEXT(x) TEXT_INNER(x)
struct failure { struct jpeg_error_mgr base; jmp_buf jump; };
static void fail(j_common_ptr object) {
    struct failure *error = (struct failure *)object->err;
    (*object->err->output_message)(object);
    longjmp(error->jump, 1);
}

int main(int argc, char **argv) {
    if (argc != 2) return 2;
    FILE *input = fopen(argv[1], "rb");
    if (input == NULL) return 2;
    struct jpeg_decompress_struct decoder = {0};
    struct failure error;
    decoder.err = jpeg_std_error(&error.base);
    error.base.error_exit = fail;
    if (setjmp(error.jump)) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 1;
    }
    jpeg_create_decompress(&decoder);
    jpeg_stdio_src(&decoder, input);
    if (jpeg_read_header(&decoder, TRUE) != JPEG_HEADER_OK || decoder.data_precision != 8 ||
        decoder.num_components != 3 || decoder.progressive_mode || !decoder.saw_JFIF_marker ||
        decoder.jpeg_color_space != JCS_YCbCr || decoder.comp_info[0].component_id != 1 ||
        decoder.comp_info[1].component_id != 2 || decoder.comp_info[2].component_id != 3) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 2;
    }
    decoder.dct_method = JDCT_ISLOW;
    decoder.do_fancy_upsampling = FALSE;
    decoder.out_color_space = JCS_RGB;
    if (!jpeg_start_decompress(&decoder) || decoder.output_components != 3) return 1;
    size_t stride = (size_t)decoder.output_width * 3;
    JSAMPARRAY row = (*decoder.mem->alloc_sarray)((j_common_ptr)&decoder, JPOOL_IMAGE, stride, 1);
    printf("{\"reference\":\"libjpeg-turbo %s JDCT_ISLOW RGB non-fancy\",\"width\":%u,\"height\":%u,\"pixels\":[",
           TEXT(LIBJPEG_TURBO_VERSION), decoder.output_width, decoder.output_height);
    int first = 1;
    while (decoder.output_scanline < decoder.output_height) {
        if (jpeg_read_scanlines(&decoder, row, 1) != 1) return 1;
        for (size_t x = 0; x < stride; x++) {
            if (!first) putchar(',');
            printf("%u", (unsigned)row[0][x]);
            first = 0;
        }
    }
    printf("]}\n");
    if (!jpeg_finish_decompress(&decoder) || error.base.num_warnings != 0) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 1;
    }
    jpeg_destroy_decompress(&decoder);
    fclose(input);
    return 0;
}
