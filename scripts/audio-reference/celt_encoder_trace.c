/* Encoder analysis and frame traces from unmodified, pinned libopus.
 * Include the translation unit to call its private MDCT analysis function.
 * The linked archive supplies the other unmodified reference components. */
#include "config.h"
#define OPUS_BUILD 1
#define VAR_ARRAYS 1
#include "celt/celt_encoder.c"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint32_t random_state;
static unsigned random_value(void) {
  random_state = random_state * UINT32_C(1664525) + UINT32_C(1013904223);
  return random_state >> 8;
}
static float random_float(float low, float high) {
  return low + (high-low) * (float)(random_value()%65536) / 65536.f;
}
static uint32_t bits(float value) {
  uint32_t result;
  memcpy(&result, &value, 4);
  return result;
}
static uint32_t hash(const float *values, int count) {
  uint32_t result = 0;
  for (int i=0; i<count; ++i) result = result*31U + bits(values[i]);
  return result;
}
static uint32_t crc(const unsigned char *bytes, int count) {
  uint32_t c = UINT32_MAX;
  for (int i=0; i<count; ++i) {
    c ^= bytes[i];
    for (int b=0; b<8; ++b) c = (c>>1)^((c&1U)?UINT32_C(0xedb88320):0U);
  }
  return ~c;
}
static void spectrum(const CELTMode *mode) {
  const int rates[] = {1,2,3,4,6};
  int cc = 1+random_value()%2, c=1;
  if (cc==2) c=1+random_value()%2;
  int lm=random_value()%4, short_blocks=0;
  if (lm>0 && random_value()%2) short_blocks=1<<lm;
  int upsample=rates[random_value()%5];
  int end=13+random_value()%9, eff_end=end;
  if (random_value()%3==0) eff_end=1+random_value()%end;
  float amplitude=random_float(1.f,30000.f);
  int n=120<<lm;
  float input[2160], freq[1920]={0}, energy[42]={0}, x[1920]={0}, logs[42]={0};
  for (int i=0;i<cc*(n+120);++i) input[i]=random_float(-amplitude,amplitude);
  compute_mdcts(mode,short_blocks,input,freq,c,cc,lm,upsample,0);
  compute_band_energies(mode,freq,energy,eff_end,c,lm,0);
  normalise_bands(mode,freq,x,energy,eff_end,c,1<<lm);
  amp2Log2(mode,eff_end,end,energy,logs,c);
  printf("m %d %d %d %d %d %d %d",cc,c,lm,short_blocks,upsample,end,eff_end);
  for(int ch=0;ch<c;++ch) printf(" %u %u %u %u",hash(freq+ch*n,n),hash(energy+ch*21,eff_end),
    hash(x+ch*n,(1<<lm)*mode->eBands[eff_end]),hash(logs+ch*21,end));
  puts("");
}
static void spread(const CELTMode *mode) {
  int c=1+random_value()%2, lm=random_value()%4, end=1+random_value()%21;
  float x[1920];
  for(int i=0;i<c*(120<<lm);++i) {
    float value=random_float(-1.f,1.f);
    if(random_value()%3==0) value*=0.05f;
    x[i]=value;
  }
  int weights[21];
  for(int i=0;i<21;++i) weights[i]=1+random_value()%32;
  int average=random_value()%512, hf=random_value()%40, tapset=random_value()%3;
  int last=random_value()%4, update=random_value()%2;
  int decision=spreading_decision(mode,x,&average,last,&hf,&tapset,update,end,c,1<<lm,weights);
  printf("s %d %d %d %d %d | %d %d %d %d\n",c,lm,end,last,update,decision,average,hf,tapset);
}
static void hysteresis(void) {
  int n=1+random_value()%8;
  float t=random_float(0.f,10.f), thresholds[8], margins[8];
  for(int i=0;i<n;++i) {
    float step=random_float(0.1f,5.f), h=random_float(0.f,1.f);
    t+=step;
    thresholds[i]=t;
    margins[i]=h;
  }
  float value=random_float(0.f,t+5.f);
  int previous=random_value()%(n+1);
  int result=hysteresis_decision(value,thresholds,margins,n,previous);
  printf("h %d %u %d %d\n",n,bits(value),previous,result);
}
static void frames(int cases, int upsample, int end) {
  for(int k=0;k<cases;++k) {
    int cc=1+random_value()%2, rate_mode=random_value()%3;
    int bitrate=6000+random_value()%250000, complexity=random_value()%11, loss=0;
    if(random_value()%4==0) loss=random_value()%20;
    int error;
    CELTEncoder *st=calloc(1,(size_t)celt_encoder_get_size(cc));
    if(!st) exit(3);
    error=celt_encoder_init(st,48000,cc,0);
    if(error) exit(3);
    st->signalling=0;
    st->upsample=upsample; st->end=end; st->bitrate=bitrate;
    st->vbr=rate_mode!=0; st->constrained_vbr=rate_mode==2; st->complexity=complexity; st->loss_rate=loss;
    int lm=random_value()%4, period=8+random_value()%1000;
    float pattern[2016];
    for(int i=0;i<cc*period;++i) pattern[i]=random_float(-1.f,1.f);
    float noise=random_float(0.f,0.5f);
    int count=1+random_value()%6, max_bytes=2+random_value()%1274;
    printf("c %d %d %d %d %d %d %d %d\n",cc,rate_mode,bitrate,complexity,loss,lm,count,max_bytes);
    int t=0, n=(120<<lm)/upsample;
    for(int f=0;f<count;++f) {
      float gain=0.f, pcm[1920];
      if(random_value()%5!=0) gain=random_float(0.f,1.f);
      for(int i=0;i<n;++i) {
        for(int c=0;c<cc;++c) {
          float value=random_float(-1.f,1.f);
          pcm[i*cc+c]=gain*(0.7f*pattern[c*period+t%period]+noise*value);
        }
        ++t;
      }
      unsigned char packet[1276];
      int length=celt_encode_with_ec(st,pcm,n,packet,max_bytes,NULL);
      uint32_t checksum=length<0?0:crc(packet,length);
      printf("f %d %u %u\n",length,checksum,st->rng);
    }
    free(st);
  }
}
int main(int argc, char **argv) {
  if(argc!=6) return 2;
  random_state=(uint32_t)strtoul(argv[2],NULL,10);
  int cases=atoi(argv[3]), error;
  const CELTMode *mode=opus_custom_mode_create(48000,960,&error);
  if(error || !mode) return 3;
  if(!strcmp(argv[1],"encode")) frames(cases,atoi(argv[4]),atoi(argv[5]));
  else if(!strcmp(argv[1],"analysis")) {
    for(int i=0;i<cases;++i) {
      unsigned which=random_value()%3;
      if(which==0) spectrum(mode); else if(which==1) spread(mode); else hysteresis();
    }
  } else return 2;
  return 0;
}
