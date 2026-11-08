/* Generate and read pages with unmodified libogg 1.3.6. */
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "ogg/ogg.h"

typedef struct {
    int active, serial, incomplete;
    uint64_t first, page;
    ogg_stream_state decoder;
} observer;

static observer streams[16];
static ogg_sync_state sync_state;
static int case_number;
static uint64_t position;

static void check(int value) {
    if (value != 0) { fprintf(stderr,"libogg error %d\n",value);exit(2); }
}
static void hex(const unsigned char *bytes, long length) {
    for (long i=0;i<length;i++) printf("%02x",bytes[i]);
}
static observer *stream_for(ogg_page *page) {
    int serial=ogg_page_serialno(page);
    for (int i=0;i<16;i++) if (streams[i].active && streams[i].serial==serial) return &streams[i];
    for (int i=0;i<16;i++) if (!streams[i].active) {
        observer *stream=&streams[i];
        memset(stream,0,sizeof(*stream));stream->active=1;stream->serial=serial;
        check(ogg_stream_init(&stream->decoder,serial));
        return stream;
    }
    exit(3);
}
static void emit(ogg_page *source) {
    long length=source->header_len+source->body_len;
    char *buffer=ogg_sync_buffer(&sync_state,length);
    if (!buffer) exit(4);
    memcpy(buffer,source->header,(size_t)source->header_len);
    memcpy(buffer+source->header_len,source->body,(size_t)source->body_len);
    check(ogg_sync_wrote(&sync_state,length));
    ogg_page page;
    if (ogg_sync_pageout(&sync_state,&page)!=1) exit(5);
    observer *stream=stream_for(&page);
    uint64_t offsets[255], starts[255];
    int count=0;
    long body=0;
    for (int i=27;i<page.header_len;i++) {
        if (!stream->incomplete) { stream->first=position+(uint64_t)page.header_len+(uint64_t)body;stream->page=position; }
        body+=page.header[i];stream->incomplete=page.header[i]==255;
        if (!stream->incomplete) { offsets[count]=stream->first;starts[count++]=stream->page; }
    }
    check(ogg_stream_pagein(&stream->decoder,&page));
    printf("{\"case\":%d,\"offset\":%" PRIu64 ",\"serial\":%u,\"sequence\":%u,\"granule\":%" PRId64 ",\"flags\":%u,\"bytes\":\"",
           case_number,position,(unsigned)ogg_page_serialno(&page),(unsigned)ogg_page_pageno(&page),(int64_t)ogg_page_granulepos(&page),page.header[5]);
    hex(page.header,page.header_len);hex(page.body,page.body_len);
    printf("\",\"packets\":[");
    int index=0,result;
    ogg_packet packet;
    while ((result=ogg_stream_packetout(&stream->decoder,&packet))!=0) {
        if (result<0 || index>=count) exit(6);
        printf("%s{\"offset\":%" PRIu64 ",\"page_offset\":%" PRIu64 ",\"bytes\":\"",index?",":"",offsets[index],starts[index]);
        hex(packet.packet,packet.bytes);printf("\"}");index++;
    }
    if (index!=count) exit(7);
    puts("]}");
    position+=(uint64_t)length;
    if (ogg_page_eos(&page)) { check(ogg_stream_clear(&stream->decoder));stream->active=0; }
}
static void begin(int id) {
    case_number=id;position=0;
    check(ogg_sync_init(&sync_state));
}
static void end(void) {
    for (int i=0;i<16;i++) if (streams[i].active) exit(8);
    check(ogg_sync_clear(&sync_state));
}
static void packet_in(ogg_stream_state *encoder, long length, int number, int eos) {
    unsigned char *data=calloc((size_t)length+1,1);
    if (!data) exit(4);
    for (long i=0;i<length;i++) data[i]=(unsigned char)(i+37*number+(i>>8));
    ogg_packet packet;
    memset(&packet,0,sizeof(packet));packet.packet=data;packet.bytes=length;
    packet.packetno=number;packet.b_o_s=number==0;packet.e_o_s=eos;packet.granulepos=960*(number+1);
    check(ogg_stream_packetin(encoder,&packet));free(data);
}
static void flush(ogg_stream_state *encoder) {
    ogg_page page;
    while (ogg_stream_flush(encoder,&page)) emit(&page);
}
static void sequential(int id, const long *lengths, int count) {
    begin(id);
    ogg_stream_state encoder;
    check(ogg_stream_init(&encoder,1234+id));
    for (int i=0;i<count;i++) { packet_in(&encoder,lengths[i],i,i==count-1);flush(&encoder); }
    check(ogg_stream_clear(&encoder));end();
}
static void little(unsigned char *bytes,uint64_t value,int count) {
    for (int i=0;i<count;i++) bytes[i]=(unsigned char)(value>>(8*i));
}
int main(void) {
    const long small[]={0,1,254,255,256};
    const long large[]={16,65024,65025,65026,131071,0,32};
    sequential(0,small,5);sequential(1,large,7);
    begin(2);
    ogg_stream_state a,b;
    check(ogg_stream_init(&a,11));check(ogg_stream_init(&b,12));
    packet_in(&a,16,0,0);flush(&a);packet_in(&b,16,0,0);flush(&b);
    packet_in(&a,70000,1,1);packet_in(&b,70000,1,1);
    ogg_page page;
    if (!ogg_stream_flush(&a,&page)) return 9;
    emit(&page);
    if (!ogg_stream_flush(&b,&page)) return 9;
    emit(&page);
    flush(&a);flush(&b);
    check(ogg_stream_clear(&a));check(ogg_stream_clear(&b));end();
    begin(3);
    for (int i=0;i<32;i++) {
        check(ogg_stream_init(&a,90+i));packet_in(&a,32,i,1);flush(&a);check(ogg_stream_clear(&a));
    }
    end();
    begin(4);
    for (int i=0;i<2;i++) {
        unsigned char header[27]={0};
        memcpy(header,"OggS",4);header[5]=i==0?2:4;
        little(header+6,i==0?UINT64_MAX:0,8);little(header+14,99,4);little(header+18,(uint64_t)i,4);
        page.header=header;page.header_len=27;page.body=header;page.body_len=0;
        ogg_page_checksum_set(&page);emit(&page);
    }
    end();
    return 0;
}
