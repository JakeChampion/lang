/* Offline grayscale IDCT observations. This is not a Fern runtime dependency. */
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
    if (jpeg_read_header(&decoder, TRUE) != JPEG_HEADER_OK || decoder.num_components != 1 || decoder.data_precision != 8) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 2;
    }
    decoder.dct_method = JDCT_ISLOW;
    decoder.out_color_space = JCS_GRAYSCALE;
    decoder.do_fancy_upsampling = FALSE;
    JQUANT_TBL *quantizers = decoder.quant_tbl_ptrs[decoder.comp_info[0].quant_tbl_no];
    if (quantizers == NULL || !jpeg_start_decompress(&decoder)) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 1;
    }
    printf("{\"reference\":\"libjpeg-turbo %s JDCT_ISLOW\",\"width\":%u,\"height\":%u,\"quantizers_natural\":[",
           TEXT(LIBJPEG_TURBO_VERSION), decoder.output_width, decoder.output_height);
    for (int i = 0; i < DCTSIZE2; i++) {
        if (i != 0) putchar(',');
        printf("%u", (unsigned)quantizers->quantval[i]);
    }
    printf("],\"pixels\":[");
    JSAMPARRAY row = (*decoder.mem->alloc_sarray)((j_common_ptr)&decoder, JPOOL_IMAGE, decoder.output_width, 1);
    int first = 1;
    while (decoder.output_scanline < decoder.output_height) {
        if (jpeg_read_scanlines(&decoder, row, 1) != 1) {
            jpeg_destroy_decompress(&decoder);
            fclose(input);
            return 1;
        }
        for (JDIMENSION x = 0; x < decoder.output_width; x++) {
            if (!first) putchar(',');
            first = 0;
            printf("%u", (unsigned)row[0][x]);
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
