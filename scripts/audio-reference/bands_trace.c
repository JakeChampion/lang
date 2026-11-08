/* Trace allocation and band quantization in unmodified, pinned libopus. */
#include "config.h"
#define VAR_ARRAYS 1
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "celt/modes.h"
#include "celt/bands.h"
#include "celt/rate.h"
#include "celt/entenc.h"
#include "celt/entdec.h"

static uint32_t random_state;
static unsigned random_value(void) {
    random_state = random_state * UINT32_C(1664525) + UINT32_C(1013904223);
    return random_state >> 8;
}
static float random_float(float low, float high) {
    return low + (high-low) * (float)(random_value()%65536) / 65536.0f;
}
static void dump(const char *label, const float *values, int from, int to) {
    printf("%s",label);
    for (int i=from;i<to;i++) { int32_t bits;memcpy(&bits,&values[i],4);printf(" %d",bits); }
    puts("");
}
static void masks(const unsigned char *values, int start, int end, int channels) {
    for (int i=start;i<end;i++) for (int c=0;c<channels;c++) printf(" %u",values[i*channels+c]);
    puts("");
}
int main(int argc, char **argv) {
    if (argc != 3) return 2;
    random_state = (uint32_t)strtoul(argv[1],NULL,10);
    int cases=atoi(argv[2]), error;
    const CELTMode *mode=opus_custom_mode_create(48000,960,&error);
    if (error || !mode) return 3;
    const int tf[] = {0,-1,0,-1,0,-1,0,-1,0,-1,0,-2,1,0,1,-1,0,-2,0,-3,2,0,1,-1,0,-2,0,-3,3,0,1,-1};
    for (int iteration=0;iteration<cases;iteration++) {
        int lm=random_value()%4;
        int channels=1+random_value()%2;
        int start=random_value()%4==0?17:0;
        int end=start+2+random_value()%(20-start);
        if (end>21) end=21;
        int m=1<<lm;
        int bytes=8+random_value()%300;
        int short_blocks=lm>0 ? random_value()%2 : 0;
        int tf_select=random_value()%2;
        int spread=random_value()%4;
        int complexity=random_value()%11;
        int disable_inv=random_value()%2;
        opus_uint32 seed=random_value(), decode_seed=seed;
        float x[960]={0}, y[960]={0}, energy[42]={0};
        for (int c=0;c<channels;c++) for (int i=0;i<21;i++) {
            int lo=m*mode->eBands[i], hi=m*mode->eBands[i+1];
            float *values=c==0?x:y, sum=0;
            for (int j=lo;j<hi;j++) { values[j]=random_float(-1.0f,1.0f);sum+=values[j]*values[j]; }
            float gain=1.0f/(float)sqrt(sum+1e-15f);
            for (int j=lo;j<hi;j++) values[j]*=gain;
            energy[21*c+i]=random_float(0.01f,100.0f);
        }
        int tf_res[21], offsets[21]={0}, cap[21];
        for (int i=0;i<21;i++) tf_res[i]=tf[8*lm+4*short_blocks+2*tf_select+random_value()%2];
        for (int i=0;i<21;i++) {
            int width=(mode->eBands[i+1]-mode->eBands[i])<<lm;
            cap[i]=(mode->cache.caps[21*(2*lm+channels-1)+i]+64)*channels*width>>2;
        }
        int trim=random_value()%11;
        int intensity=start+random_value()%(end+1-start);
        int dual=channels==2?random_value()%2:0;
        int pulses[21]={0}, fine[21]={0}, priority[21]={0};
        opus_int32 balance=0;
        unsigned char packet[307]={0}, collapse[42]={0};
        ec_enc encoder;
        ec_enc_init(&encoder,packet,bytes);
        int total=bytes*8<<3;
        int coded=clt_compute_allocation(mode,start,end,offsets,cap,trim,&intensity,&dual,total-(int)ec_tell_frac(&encoder)-1,&balance,pulses,fine,priority,channels,lm,&encoder,1,0,20);
        quant_all_bands(1,mode,start,end,x,channels==2?y:NULL,collapse,energy,pulses,short_blocks,spread,dual,intensity,tf_res,total,balance,&encoder,lm,coded,&seed,complexity,0,disable_inv);
        printf("c %d %d %d %d %d %d %d %d %d coded %d tell %u seed %u |",lm,channels,start,end,bytes,short_blocks,spread,complexity,disable_inv,coded,ec_tell_frac(&encoder),seed);
        masks(collapse,start,end,channels);
        dump("ex",x,m*mode->eBands[start],m*mode->eBands[end]);
        if (channels==2) dump("ey",y,m*mode->eBands[start],m*mode->eBands[end]);
        ec_enc_done(&encoder);
        ec_dec decoder;
        ec_dec_init(&decoder,packet,bytes);
        intensity=dual=0;
        coded=clt_compute_allocation(mode,start,end,offsets,cap,trim,&intensity,&dual,total-(int)ec_tell_frac(&decoder)-1,&balance,pulses,fine,priority,channels,lm,&decoder,0,0,0);
        memset(x,0,sizeof(x));memset(y,0,sizeof(y));memset(collapse,0,sizeof(collapse));
        quant_all_bands(0,mode,start,end,x,channels==2?y:NULL,collapse,energy,pulses,short_blocks,spread,dual,intensity,tf_res,total,balance,&decoder,lm,coded,&decode_seed,complexity,0,disable_inv);
        printf("d coded %d tell %u seed %u |",coded,ec_tell_frac(&decoder),decode_seed);
        masks(collapse,start,end,channels);
        dump("dx",x,m*mode->eBands[start],m*mode->eBands[end]);
        if (channels==2) dump("dy",y,m*mode->eBands[start],m*mode->eBands[end]);
    }
    return 0;
}
