/* Cold seeks through unmodified libopusfile, on independently muxed Opus files. */
#define main opus_file_reference_main
#include "opus_file.c"
#undef main
#include <math.h>

static uint32_t checksum(const unsigned char *bytes, size_t count) {
  uint32_t c=UINT32_MAX;
  for(size_t i=0;i<count;++i) {
    c^=bytes[i];
    for(int bit=0;bit<8;++bit) c=(c>>1)^((c&1U)?UINT32_C(0xedb88320):0U);
  }
  return ~c;
}

static void collect(OggOpusFile *decoder, Bytes pcm[8]) {
  for(;;) {
    float samples[46080];
    int link=-1;
    int frames=op_read_float(decoder,samples,46080,&link);
    check(frames);
    if(!frames) return;
    int channels=op_head(decoder,link)->channel_count;
    append(&pcm[link],samples,(size_t)frames*(unsigned)channels*sizeof(float));
  }
}

static void emit_seek(int id, const Bytes *file) {
  int error;
  OggOpusFile *decoder=op_open_memory(file->data,file->length,&error);
  check(error);
  if(!decoder) exit(4);
  int links=op_link_count(decoder);
  if(links<1||links>8) exit(5);
  int64_t frames[8], global=0, total=op_pcm_total(decoder,-1);
  Bytes full[8]={{0}};
  for(int i=0;i<links;++i) frames[i]=op_pcm_total(decoder,i);
  collect(decoder,full);
  op_free(decoder);
  printf("{\"case\":%d,\"file_bytes\":%zu,\"file_crc\":%u,\"pcm_crc\":[",id,file->length,checksum(file->data,file->length));
  for(int i=0;i<links;++i) printf("%s%u",i?",":"",checksum(full[i].data,full[i].length));
  printf("],\"points\":[");
  int point=0;
  for(int li=0;li<links;++li) {
    int64_t probes[]={0,frames[li],frames[li]/2,frames[li]-1,1,frames[li]*3/4,3840,4160};
    for(size_t p=0;p<sizeof(probes)/sizeof(probes[0]);++p) {
      int64_t position=probes[p];
      if(position<0||position>frames[li]) continue;
      decoder=op_open_memory(file->data,file->length,&error);
      check(error);
      if(!decoder) exit(4);
      Bytes pcm[8]={{0}};
      int result=op_pcm_seek(decoder,global+position);
      /* The Fern cursor also permits the exact end of an empty whole file. */
      if(result<0 && global+position!=total) check(result);
      if(result>=0) {
        if(op_pcm_tell(decoder)!=global+position) exit(7);
        collect(decoder,pcm);
      }
      double maximum=0;
      printf("%s{\"link\":%d,\"position\":%lld,\"pcm\":[",point++?",":"",li,(long long)position);
      int first=1;
      for(int i=li;i<links;++i) {
        int channels=op_head(decoder,i)->channel_count;
        size_t start=i==li?(size_t)position*(unsigned)channels*sizeof(float):0;
        if(start>full[i].length||pcm[i].length!=full[i].length-start) exit(8);
        for(size_t s=0;s<pcm[i].length/sizeof(float);++s) {
          uint32_t bits;
          float value, baseline;
          memcpy(&bits,pcm[i].data+s*4,4);
          memcpy(&value,pcm[i].data+s*4,4);
          memcpy(&baseline,full[i].data+start+s*4,4);
          double difference=fabs((double)value-(double)baseline);
          if(difference>maximum) maximum=difference;
          printf("%s%u",first?"":",",bits);
          first=0;
        }
        free(pcm[i].data);
      }
      printf("],\"full_max_abs\":%.17g}",maximum);
      op_free(decoder);
    }
    global+=frames[li];
  }
  puts("]}");
  for(int i=0;i<links;++i) free(full[i].data);
}

int main(void) {
  Bytes files[8];
  for(int i=0;i<8;++i) { files[i]=make_file(i);emit_seek(i,&files[i]); }
  Bytes chain={0};
  for(int i=0;i<3;++i) append(&chain,files[i].data,files[i].length);
  emit_seek(8,&chain);
  free(chain.data);
  Bytes empty=make_file(10);
  emit_seek(10,&empty);
  free(empty.data);
  for(int i=0;i<8;++i) free(files[i].data);
  return 0;
}
