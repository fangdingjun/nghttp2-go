#ifndef _NGHTTP2_H
#define _NGHTTP2_H
#include <nghttp2/nghttp2.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#define ARRLEN(x) (sizeof(x) / sizeof(x[0]))

extern ssize_t onDataRecvCallback(void *, void *data, size_t);
extern ssize_t onDataSendCallback(void *, void *data, size_t);
extern ssize_t onServerDataSourceReadCallback(void *, int, void *, size_t);
extern ssize_t onDataSourceReadCallback(void *, int, void *, size_t);
extern int onDataChunkRecv(void *, int, void *, size_t);
extern int onBeginHeaderCallback(void *, int);
extern int onHeaderCallback(void *, int, void *, int, void *, int);
extern int onHeadersDoneCallback(void *, int);
extern int onStreamClose(void *, int);
extern void onConnectionCloseCallback(void *user_data);
extern void onStreamEndCallback(void *, int);

int _nghttp2_submit_response(nghttp2_session *sess, int streamid,
                             size_t nv, size_t nvlen, nghttp2_data_provider *dp);

int _nghttp2_submit_request(nghttp2_session *session, const nghttp2_priority_spec *pri_spec,
                            size_t nva, size_t nvlen,
                            const nghttp2_data_provider *data_prd, void *stream_user_data);

int send_connection_header(nghttp2_session *session);

struct nv_array
{
    nghttp2_nv *nv;
    size_t len;
};

void delete_nv_array(struct nv_array *a);
int data_provider_set_callback(size_t dp, size_t data, int t);

int nv_array_set(struct nv_array *a, int index,
                 char *name, char *value,
                 size_t namelen, size_t valuelen, int flag);

struct nv_array *new_nv_array(size_t n);

//int32_t submit_request(nghttp2_session *session, nghttp2_nv *hdrs, size_t hdrlen,
//                       nghttp2_data_provider *dp);

//int send_client_connection_header(nghttp2_session *session);

nghttp2_session *init_nghttp2_client_session(size_t data);
nghttp2_session *init_nghttp2_server_session(size_t data);

#endif