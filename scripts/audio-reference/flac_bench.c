#define _POSIX_C_SOURCE 200809L
#include <FLAC/stream_encoder.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct { unsigned char *data; size_t at, size, capacity; } Buffer;
static FLAC__StreamEncoderWriteStatus write_bytes(const FLAC__StreamEncoder *e, const FLAC__byte *data, size_t n, uint32_t samples, uint32_t frame, void *ctx) {
    (void)e; (void)samples; (void)frame;
    Buffer *b=ctx;
    if (n>SIZE_MAX-b->at) return FLAC__STREAM_ENCODER_WRITE_STATUS_FATAL_ERROR;
    size_t end=b->at+n;
    if (end>b->capacity) {
        size_t capacity=end<4096?4096:end*2;
        void *p=realloc(b->data,capacity);
        if (!p) return FLAC__STREAM_ENCODER_WRITE_STATUS_FATAL_ERROR;
        b->data=p; b->capacity=capacity;
    }
    memcpy(b->data+b->at,data,n); b->at=end;
    if (end>b->size) b->size=end;
    return FLAC__STREAM_ENCODER_WRITE_STATUS_OK;
}
static FLAC__StreamEncoderSeekStatus seek_bytes(const FLAC__StreamEncoder *e, FLAC__uint64 at, void *ctx) {
    (void)e;Buffer *b=ctx;
    if (at>b->size) return FLAC__STREAM_ENCODER_SEEK_STATUS_ERROR;
    b->at=(size_t)at;return FLAC__STREAM_ENCODER_SEEK_STATUS_OK;
}
static FLAC__StreamEncoderTellStatus tell_bytes(const FLAC__StreamEncoder *e, FLAC__uint64 *at, void *ctx) {
    (void)e;*at=((Buffer *)ctx)->at;return FLAC__STREAM_ENCODER_TELL_STATUS_OK;
}
static int encode(const int32_t *pcm, unsigned frames, unsigned bits, unsigned channels, Buffer *b) {
    FLAC__StreamEncoder *e=FLAC__stream_encoder_new();
    if (!e) return 0;
    int good=FLAC__stream_encoder_set_streamable_subset(e,0)
      && FLAC__stream_encoder_set_compression_level(e,5)
      && FLAC__stream_encoder_set_channels(e,channels)
      && FLAC__stream_encoder_set_bits_per_sample(e,bits)
      && FLAC__stream_encoder_set_sample_rate(e,48000)
      && FLAC__stream_encoder_set_blocksize(e,4096)
      && FLAC__stream_encoder_set_max_lpc_order(e,0)
      && FLAC__stream_encoder_set_min_residual_partition_order(e,0)
      && FLAC__stream_encoder_set_max_residual_partition_order(e,6)
      && FLAC__stream_encoder_set_do_mid_side_stereo(e,channels==2)
      && FLAC__stream_encoder_set_loose_mid_side_stereo(e,0)
      && FLAC__stream_encoder_set_do_exhaustive_model_search(e,1)
      && FLAC__stream_encoder_set_total_samples_estimate(e,frames);
    good=good && FLAC__stream_encoder_init_stream(e,write_bytes,seek_bytes,tell_bytes,NULL,b)==FLAC__STREAM_ENCODER_INIT_STATUS_OK;
    good=good && FLAC__stream_encoder_process_interleaved(e,pcm,frames);
    int finished=FLAC__stream_encoder_finish(e);
    FLAC__stream_encoder_delete(e);
    return good && finished;
}
static long long nanoseconds(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC,&t);return (long long)t.tv_sec*1000000000LL+t.tv_nsec; }
static long peak_rss(void) {
    FILE *f=fopen("/proc/self/status","r");if (!f) return 0;
    char line[256];long rss=0;
    while (fgets(line,sizeof(line),f)) if (sscanf(line,"VmHWM: %ld kB",&rss)==1) break;
    fclose(f);return rss;
}
int main(int argc,char **argv) {
    if (argc!=7 || strcmp(argv[6],"fixed")) return 1;
    int bits=atoi(argv[3]),channels=atoi(argv[4]),rounds=atoi(argv[5]);
    if (bits<4 || bits>32 || channels<1 || channels>8 || rounds<1) return 1;
    FILE *f=fopen(argv[1],"rb");if (!f || fseek(f,0,SEEK_END)) return 2;
    long bytes=ftell(f);if (bytes<4 || bytes%(4*channels) || fseek(f,0,SEEK_SET)) return 2;
    int32_t *pcm=malloc((size_t)bytes);if (!pcm || fread(pcm,1,(size_t)bytes,f)!=(size_t)bytes) return 2;fclose(f);
    unsigned frames=(unsigned)(bytes/(4*channels));
    for (int i=0;i<2;i++) {
        Buffer b={0};if (!encode(pcm,frames,bits,channels,&b)) return 3;
        if (!i) {f=fopen(argv[2],"wb");if (!f || fwrite(b.data,1,b.size,f)!=b.size || fclose(f)) return 4;}
        free(b.data);
    }
    uint32_t hash=2166136261u;uint64_t total=0;
    long long started=nanoseconds();
    for (int i=0;i<rounds;i++) {
        Buffer b={0};if (!encode(pcm,frames,bits,channels,&b)) return 5;
        for (size_t j=0;j<b.size;j++) hash=(hash^b.data[j])*16777619u;
        total+=b.size;free(b.data);
    }
    long long elapsed=nanoseconds()-started;
    printf("wall_ns=%lld\nchecksum=%u\nencoded_bytes=%llu\nnative_peak_rss_kb=%ld\n",elapsed,hash,(unsigned long long)total,peak_rss());
    free(pcm);return 0;
}
