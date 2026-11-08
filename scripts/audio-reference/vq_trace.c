/* Trace the unmodified vector quantizer in pinned libopus. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "celt/vq.h"
#include "celt/entenc.h"
#include "celt/entdec.h"

static uint32_t seed;
static uint64_t counts[177][177];
static const int last[] = {176,176,176,176,176,176,96,54,37,28,24,19,18,16,14};
static unsigned random_value(void) {
    seed = seed * UINT32_C(1664525) + UINT32_C(1013904223);
    return seed >> 8;
}
static float random_float(float low, float high) {
    return low + (high-low) * (float)(random_value()%65536) / 65536.0f;
}
static int reach(int a, int b) {
    int low = a < b ? a : b, high = a > b ? a : b;
    return low <= 14 && high <= last[low];
}
static int fits(int n, int k) {
    return reach(n,k) && reach(n,k+1) && counts[n][k]+counts[n][k+1] < UINT64_C(4294967296);
}
static void bits(const float *values, int count) {
    for (int i = 0; i < count; i++) {
        int32_t value;
        memcpy(&value, &values[i], sizeof(value));
        printf(" %d", value);
    }
}
int main(int argc, char **argv) {
    if (argc != 3) return 2;
    seed = (uint32_t)strtoul(argv[1], NULL, 10);
    int cases = atoi(argv[2]);
    if (cases < 1 || cases > 2000) return 2;
    counts[0][0]=1;
    for (int n=1;n<177;n++) for (int k=1;k<177;k++) {
        uint64_t sum=counts[n-1][k]+counts[n][k-1]+counts[n-1][k-1];
        counts[n][k]=sum < UINT64_C(4294967296) ? sum : UINT64_C(4294967296);
    }
    int ns[2000], ks[2000], spreads[2000], blocks[2000];
    float gains[2000];
    unsigned char packet[60000] = {0};
    ec_enc encoder;
    ec_enc_init(&encoder, packet, sizeof(packet));
    for (int c = 0; c < cases; c++) {
        int short_band = random_value()%2;
        int n = 2 + random_value()%(short_band ? 15 : 175);
        int k = 1 + random_value()%128;
        while (!fits(n,k)) k = 1 + random_value()%128;
        int b = 1 << (random_value()%4);
        while (n%b != 0) b >>= 1;
        int spread = random_value()%4;
        int resynth = random_value()%2;
        float gain = random_float(0.25f, 2.0f);
        float x[176], y[176];
        for (int i = 0; i < n; i++) {
            x[i] = random_float(-1.0f,1.0f);
            if (random_value()%4 == 0) x[i] = 0;
            y[i] = random_float(-1.0f,1.0f);
        }
        unsigned mask = alg_quant(x,n,k,spread,b,&encoder,gain,resynth,0);
        printf("q %d %d %d %d %d %u tell %u |", n,k,spread,b,resynth,mask,ec_tell_frac(&encoder));
        bits(x,n);
        puts("");
        renormalise_vector(y,n,gain,0);
        printf("r");
        bits(y,n);
        printf(" t %d %d\n",stereo_itheta(x,y,1,n,0)>>16,stereo_itheta(x,y,0,n,0)>>16);
        ns[c]=n; ks[c]=k; spreads[c]=spread; blocks[c]=b; gains[c]=gain;
    }
    ec_enc_done(&encoder);
    ec_dec decoder;
    ec_dec_init(&decoder,packet,sizeof(packet));
    for (int c = 0; c < cases; c++) {
        float x[176];
        unsigned mask = alg_unquant(x,ns[c],ks[c],spreads[c],blocks[c],&decoder,gains[c]);
        printf("u %u |",mask);
        bits(x,ns[c]);
        puts("");
    }
    return 0;
}
