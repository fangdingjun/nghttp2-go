#ifndef _NGHTTP2_H
#define _NGHTTP2_H
#include <nghttp2/nghttp2.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

extern ssize_t DataRead(void *, void *data, size_t);
extern ssize_t DataWrite(void *, void *data, size_t);
extern ssize_t DataSourceRead(void *, void *, size_t);
extern int OnDataRecv(void *, int, void *, size_t);
extern int OnBeginHeaderCallback(void *, int);
extern int OnHeaderCallback(void *, int, void *, int, void *, int);
extern int OnFrameRecvCallback(void *, int);
extern int OnStreamClose(void *, int);

struct nv_array
{
    nghttp2_nv *nv;
    size_t len;
};

void delete_nv_array(struct nv_array *a);
nghttp2_data_provider *new_data_provider(void *data);

int nv_array_set(struct nv_array *a, int index,
                 char *name, char *value,
                 size_t namelen, size_t valuelen, int flag);

struct nv_array *new_nv_array(size_t n);

int32_t submit_request(nghttp2_session *session, nghttp2_nv *hdrs, size_t hdrlen);

int send_client_connection_header(nghttp2_session *session);

nghttp2_session * init_nghttp2_session(size_t data);

#endif