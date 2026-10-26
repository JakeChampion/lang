/* Offline coefficient oracle. This file is not part of the Fern runtime. */
#include <stdio.h>
#include <stdlib.h>
#include <setjmp.h>
#include <jpeglib.h>

#define TEXT_INNER(x) #x
#define TEXT(x) TEXT_INNER(x)

struct failure {
    struct jpeg_error_mgr base;
    jmp_buf jump;
};

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
    jpeg_read_header(&decoder, TRUE);
    jvirt_barray_ptr *arrays = jpeg_read_coefficients(&decoder);
    if (arrays == NULL || error.base.num_warnings != 0) {
        jpeg_destroy_decompress(&decoder);
        fclose(input);
        return 1;
    }
    printf("{\"reference\":\"libjpeg-turbo %s\",\"width\":%u,\"height\":%u,\"components\":[",
           TEXT(LIBJPEG_TURBO_VERSION), decoder.image_width, decoder.image_height);
    for (int i = 0; i < decoder.num_components; i++) {
        jpeg_component_info *component = &decoder.comp_info[i];
        if (i != 0) putchar(',');
        printf("{\"id\":%d,\"horizontal\":%d,\"vertical\":%d,\"columns\":%u,\"rows\":%u,\"blocks\":[",
               component->component_id, component->h_samp_factor, component->v_samp_factor,
               component->width_in_blocks, component->height_in_blocks);
        for (JDIMENSION y = 0; y < component->height_in_blocks; y++) {
            JBLOCKARRAY row = (*decoder.mem->access_virt_barray)((j_common_ptr)&decoder, arrays[i], y, 1, FALSE);
            for (JDIMENSION x = 0; x < component->width_in_blocks; x++) {
                if (y != 0 || x != 0) putchar(',');
                putchar('[');
                for (int k = 0; k < DCTSIZE2; k++) {
                    if (k != 0) putchar(',');
                    printf("%d", (int)row[0][x][k]);
                }
                putchar(']');
            }
        }
        printf("]}");
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
