/* Use one FFmpeg decoder thread and immutable, padded input packets. */
#define _POSIX_C_SOURCE 200809L
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <libavcodec/avcodec.h>
#include <libavcodec/version.h>
#include <libavutil/buffer.h>
#include <libavutil/pixfmt.h>

#define MAX_PACKETS 64
#define MAX_BYTES 67108864
#define PADDING 64
_Static_assert(AV_INPUT_BUFFER_PADDING_SIZE <= PADDING, "corpus padding is too small");

typedef struct {
    uint8_t *input;
    AVPacket *packets[MAX_PACKETS];
    uint32_t width, height, depth, count;
    AVCodecContext *decoder;
    AVFrame *frame;
} Corpus;

static void fail(const char *message) { fprintf(stderr, "%s\n", message); exit(1); }
static uint32_t le(const uint8_t *p) {
    return (uint32_t)p[0] | (uint32_t)p[1]<<8 | (uint32_t)p[2]<<16 | (uint32_t)p[3]<<24;
}
static void borrowed(void *opaque, uint8_t *data) { (void)opaque; (void)data; }
static void load(Corpus *c, const char *path) {
    FILE *file = fopen(path, "rb");
    if (!file || fseek(file, 0, SEEK_END)) fail("cannot open corpus");
    long length = ftell(file);
    if (length < 24 || length > MAX_BYTES || fseek(file, 0, SEEK_SET)) fail("invalid corpus size");
    c->input = malloc((size_t)length);
    if (!c->input || fread(c->input, 1, (size_t)length, file) != (size_t)length || fclose(file)) fail("cannot read corpus");
    uint8_t *b = c->input;
    if (memcmp(b, "FQBM", 4) || le(b+4) != 1) fail("invalid corpus header");
    c->width = le(b+8); c->height = le(b+12); c->depth = le(b+16); c->count = le(b+20);
    if (!c->width || !c->height || (uint64_t)c->width*c->height > 8388608 ||
        (c->depth != 24 && c->depth != 32) || !c->count || c->count > MAX_PACKETS) fail("invalid corpus fields");
    size_t at = 24;
    for (uint32_t i=0; i<c->count; i++) {
        if ((size_t)length-at < 4) fail("truncated corpus");
        uint32_t bytes = le(b+at); at += 4;
        if (!bytes || bytes > 16777216 || (size_t)length-at < PADDING || bytes > (size_t)length-at-PADDING) fail("invalid packet size");
        for (int j=0; j<PADDING; j++) if (b[at+bytes+j]) fail("nonzero input padding");
        AVPacket *p = av_packet_alloc();
        if (!p) fail("cannot allocate packet");
        p->buf = av_buffer_create(b+at, bytes+PADDING, borrowed, NULL, AV_BUFFER_FLAG_READONLY);
        if (!p->buf) fail("cannot reference packet");
        p->data = b+at; p->size = (int)bytes;
        c->packets[i] = p; at += bytes+PADDING;
    }
    if (at != (size_t)length) fail("trailing corpus data");
    const AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_QTRLE);
    if (!codec) fail("FFmpeg has no QTRLE decoder");
    c->decoder = avcodec_alloc_context3(codec); c->frame = av_frame_alloc();
    if (!c->decoder || !c->frame) fail("cannot allocate decoder");
    c->decoder->width = (int)c->width; c->decoder->height = (int)c->height;
    c->decoder->bits_per_coded_sample = (int)c->depth;
    c->decoder->thread_count = 1; c->decoder->thread_type = 0;
    if (avcodec_open2(c->decoder, codec, NULL) < 0) fail("cannot open decoder");
}
static void decode(Corpus *c, uint32_t packet) {
    if (avcodec_send_packet(c->decoder, c->packets[packet]) < 0 ||
        avcodec_receive_frame(c->decoder, c->frame) < 0) fail("decode failed");
    if (c->frame->width != (int)c->width || c->frame->height != (int)c->height ||
        c->frame->format != (c->depth == 24 ? AV_PIX_FMT_RGB24 : AV_PIX_FMT_ARGB) ||
        c->frame->linesize[0] < (int)(c->width*(c->depth/8))) fail("unexpected output format");
}
static uint32_t pixel(const Corpus *c, uint32_t i) {
    uint32_t x = i%c->width, y = i/c->width;
    const uint8_t *p = c->frame->data[0] + (size_t)y*c->frame->linesize[0] + x*(c->depth/8);
    if (c->depth == 32) return (uint32_t)p[0]<<24 | (uint32_t)p[1]<<16 | (uint32_t)p[2]<<8 | p[3];
    return UINT32_C(0xff000000) | (uint32_t)p[0]<<16 | (uint32_t)p[1]<<8 | p[2];
}
static int64_t now_ns(void) {
    struct timespec t;
    if (clock_gettime(CLOCK_MONOTONIC, &t)) fail("cannot read clock");
    return (int64_t)t.tv_sec*1000000000 + t.tv_nsec;
}
static void verify(Corpus *c) {
    uint8_t row[4096]; size_t used = 0;
    for (uint32_t f=0; f<c->count; f++) {
        decode(c, f);
        for (uint32_t i=0; i<c->width*c->height; i++) {
            uint32_t p = pixel(c, i);
            row[used++] = (uint8_t)(p>>24); row[used++] = (uint8_t)(p>>16);
            row[used++] = (uint8_t)(p>>8); row[used++] = (uint8_t)p;
            if (used == sizeof(row)) { if (fwrite(row, 1, used, stdout) != used) fail("write failed"); used = 0; }
        }
        av_frame_unref(c->frame);
    }
    if (used && fwrite(row, 1, used, stdout) != used) fail("write failed");
}
static void measure(Corpus *c, uint32_t frames) {
    for (uint32_t i=0; i<c->count; i++) { decode(c, i); av_frame_unref(c->frame); }
    uint64_t checksum = 0;
    int64_t start = now_ns();
    for (uint32_t i=0; i<frames; i++) {
        decode(c, i%c->count);
        checksum += pixel(c, i%(c->width*c->height));
        av_frame_unref(c->frame);
    }
    int64_t elapsed = now_ns()-start;
    printf("{\"frames\":%"PRIu32",\"checksum\":%"PRIu64",\"elapsed_ns\":%"PRId64",\"allocations\":null,\"avcodec_version\":%u,\"avcodec_header_version\":%u}\n", frames, checksum, elapsed, avcodec_version(), LIBAVCODEC_VERSION_INT);
}
int main(int argc, char **argv) {
    if (argc < 3) return 1;
    if (avcodec_version() >> 16 != LIBAVCODEC_VERSION_MAJOR) fail("FFmpeg header and library ABI differ");
    Corpus c = {0}; load(&c, argv[2]);
    if (!strcmp(argv[1], "verify") && argc == 3) verify(&c);
    else if (!strcmp(argv[1], "measure") && argc == 4) {
        char *end = NULL; unsigned long frames = strtoul(argv[3], &end, 10);
        if (!end || *end || !frames || frames > 1000000) fail("invalid frame count");
        measure(&c, (uint32_t)frames);
    } else fail("invalid mode");
    av_frame_free(&c.frame); avcodec_free_context(&c.decoder);
    for (uint32_t i=0; i<c.count; i++) av_packet_free(&c.packets[i]);
    free(c.input);
    return fflush(stdout) ? 1 : 0;
}
