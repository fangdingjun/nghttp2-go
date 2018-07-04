#ifndef _NGHTTP2_H
#define _NGHTTP2_H
#include <nghttp2/nghttp2.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#define ARRLEN(x) (sizeof(x) / sizeof(x[0]))

extern ssize_t ClientDataRecv(void *, void *data, size_t);
extern ssize_t ClientDataSend(void *, void *data, size_t);
extern ssize_t DataSourceRead(void *, void *, size_t);
extern int OnClientDataRecv(void *, int, void *, size_t);
extern int OnClientBeginHeaderCallback(void *, int);
extern int OnClientHeaderCallback(void *, int, void *, int, void *, int);
extern int OnClientHeadersDoneCallback(void *, int);
extern int OnClientStreamClose(void *, int);

extern ssize_t ServerDataRecv(void *, void *data, size_t);
extern ssize_t ServerDataSend(void *, void *data, size_t);
extern int OnServerDataRecv(void *, int, void *, size_t);
extern int OnServerBeginHeaderCallback(void *, int);
extern int OnServerHeaderCallback(void *, int, void *, int, void *, int);
extern int OnServerStreamEndCallback(void *, int);
extern int OnServerHeadersDoneCallback(void *, int);
extern int OnServerStreamClose(void *, int);
int send_server_connection_header(nghttp2_session *session);

struct nv_array
{
    nghttp2_nv *nv;
    size_t len;
};

void delete_nv_array(struct nv_array *a);
nghttp2_data_provider *new_data_provider(size_t data);

int nv_array_set(struct nv_array *a, int index,
                 char *name, char *value,
                 size_t namelen, size_t valuelen, int flag);

struct nv_array *new_nv_array(size_t n);

int32_t submit_request(nghttp2_session *session, nghttp2_nv *hdrs, size_t hdrlen,
                       nghttp2_data_provider *dp);

int send_client_connection_header(nghttp2_session *session);

nghttp2_session *init_client_session(size_t data);
nghttp2_session *init_server_session(size_t data);

#endif