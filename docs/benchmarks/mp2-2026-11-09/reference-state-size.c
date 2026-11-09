#define MINIMP3_IMPLEMENTATION
#define MINIMP3_FLOAT_OUTPUT
#include "minimp3.h"
#include <stdio.h>
int main(void) {
    mp3dec_t state;
    printf("decoder_bytes=%zu\nqmf_bytes=%zu\n", sizeof(state), sizeof(state.qmf_state));
    return 0;
}
