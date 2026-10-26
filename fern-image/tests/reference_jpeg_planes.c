/* Offline raw-component observations. No Fern runtime dependency. */
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
        (decoder.num_components != 1 && decoder.num_components != 3) || decoder.progressive_mode) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 2;
    }
    decoder.raw_data_out = TRUE;
    decoder.dct_method = JDCT_ISLOW;
    decoder.do_fancy_upsampling = FALSE;
    if (!jpeg_start_decompress(&decoder)) return 1;
    JSAMPARRAY rows[3] = {NULL, NULL, NULL};
    unsigned char *planes[3] = {NULL, NULL, NULL};
    for (int c = 0; c < decoder.num_components; c++) {
        jpeg_component_info *component = &decoder.comp_info[c];
        rows[c] = (*decoder.mem->alloc_sarray)((j_common_ptr)&decoder, JPOOL_IMAGE,
                  component->width_in_blocks * DCTSIZE, component->v_samp_factor * DCTSIZE);
        size_t size = (size_t)component->downsampled_width * component->downsampled_height;
        planes[c] = malloc(size);
        if (planes[c] == NULL) return 1;
    }
    while (decoder.output_scanline < decoder.output_height) {
        JDIMENSION first = decoder.output_scanline;
        if (jpeg_read_raw_data(&decoder, rows, decoder.max_v_samp_factor * DCTSIZE) == 0) return 1;
        for (int c = 0; c < decoder.num_components; c++) {
            jpeg_component_info *component = &decoder.comp_info[c];
            JDIMENSION top = first * component->v_samp_factor / decoder.max_v_samp_factor;
            for (int y = 0; y < component->v_samp_factor * DCTSIZE; y++) {
                if (top + y >= component->downsampled_height) break;
                for (JDIMENSION x = 0; x < component->downsampled_width; x++) {
                    planes[c][(top + y) * component->downsampled_width + x] = rows[c][y][x];
                }
            }
        }
    }
    printf("{\"reference\":\"libjpeg-turbo %s JDCT_ISLOW raw components\",\"width\":%u,\"height\":%u,\"planes\":[",
           TEXT(LIBJPEG_TURBO_VERSION), decoder.output_width, decoder.output_height);
    for (int c = 0; c < decoder.num_components; c++) {
        if (c != 0) putchar(',');
        jpeg_component_info *component = &decoder.comp_info[c];
        printf("{\"id\":%d,\"horizontal\":%d,\"vertical\":%d,\"width\":%u,\"height\":%u,\"pixels\":[",
               component->component_id, component->h_samp_factor, component->v_samp_factor,
               component->downsampled_width, component->downsampled_height);
        size_t size = (size_t)component->downsampled_width * component->downsampled_height;
        for (size_t i = 0; i < size; i++) {
            if (i != 0) putchar(',');
            printf("%u", (unsigned)planes[c][i]);
        }
        printf("]}");
        free(planes[c]);
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
