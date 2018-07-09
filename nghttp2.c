#include "_nghttp2.h"

static ssize_t server_send_callback(nghttp2_session *session,
                                    const uint8_t *data, size_t length,
                                    int flags, void *user_data)
{
    return onServerDataSendCallback(user_data, (void *)data, length);
}

static int on_server_frame_recv_callback(nghttp2_session *session,
                                         const nghttp2_frame *frame,
                                         void *user_data)
{
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_REQUEST)
        {
            onServerHeadersDoneCallback(user_data, frame->hd.stream_id);
        }
    case NGHTTP2_DATA:
        if (frame->hd.flags & NGHTTP2_FLAG_END_STREAM)
        {
            onServerStreamEndCallback(user_data, frame->hd.stream_id);
        }
        break;
    }
    return 0;
}

static int on_server_stream_close_callback(nghttp2_session *session,
                                           int32_t stream_id,
                                           uint32_t error_code,
                                           void *user_data)

{
    onServerStreamClose(user_data, stream_id);
    return 0;
}

static int on_server_header_callback(nghttp2_session *session,
                                     const nghttp2_frame *frame,
                                     const uint8_t *name, size_t namelen,
                                     const uint8_t *value,
                                     size_t valuelen, uint8_t flags,
                                     void *user_data)
{
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_REQUEST)
        {
            onServerHeaderCallback(user_data, frame->hd.stream_id,
                                   (void *)name, namelen, (void *)value, valuelen);
        }
        break;
    }
    return 0;
}

static int on_server_data_chunk_recv_callback(nghttp2_session *session,
                                              uint8_t flags,
                                              int32_t stream_id,
                                              const uint8_t *data,
                                              size_t len, void *user_data)
{
    return onServerDataChunkRecv(user_data, stream_id, (void *)data, len);
}

static int on_server_begin_headers_callback(nghttp2_session *session,
                                            const nghttp2_frame *frame,
                                            void *user_data)
{

    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_REQUEST)
        {
            onServerBeginHeaderCallback(user_data, frame->hd.stream_id);
        }
        break;
    }
    return 0;
}

nghttp2_session *init_nghttp2_server_session(size_t data)
{
    nghttp2_session_callbacks *callbacks;
    nghttp2_session *session;

    nghttp2_session_callbacks_new(&callbacks);

    nghttp2_session_callbacks_set_send_callback(callbacks, server_send_callback);

    nghttp2_session_callbacks_set_on_frame_recv_callback(callbacks,
                                                         on_server_frame_recv_callback);

    nghttp2_session_callbacks_set_on_stream_close_callback(
        callbacks, on_server_stream_close_callback);

    nghttp2_session_callbacks_set_on_data_chunk_recv_callback(
        callbacks, on_server_data_chunk_recv_callback);
    nghttp2_session_callbacks_set_on_header_callback(callbacks,
                                                     on_server_header_callback);

    nghttp2_session_callbacks_set_on_begin_headers_callback(
        callbacks, on_server_begin_headers_callback);

    nghttp2_session_server_new(&session, callbacks, (void *)((int *)(data)));

    nghttp2_session_callbacks_del(callbacks);
    return session;
}

int send_server_connection_header(nghttp2_session *session)
{
    nghttp2_settings_entry iv[1] = {
        {NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS, 100}};
    int rv;

    rv = nghttp2_submit_settings(session, NGHTTP2_FLAG_NONE, iv,
                                 ARRLEN(iv));
    return rv;
    /*
  if (rv != 0) {
    // warnx("Fatal error: %s", nghttp2_strerror(rv));
    return rv;
  }
  return 0;
  */
}

// send_callback send data to network
static ssize_t client_send_callback(nghttp2_session *session, const uint8_t *data,
                                    size_t length, int flags, void *user_data)
{
    return onClientDataSendCallback(user_data, (void *)data, length);
}

// recv_callback read data from network
static ssize_t client_recv_callback(nghttp2_session *session, uint8_t *buf,
                                    size_t length, int flags, void *user_data)
{
    return onClientDataRecvCallback(user_data, (void *)buf, length);
}

static int on_client_header_callback(nghttp2_session *session,
                                     const nghttp2_frame *frame, const uint8_t *name,
                                     size_t namelen, const uint8_t *value,
                                     size_t valuelen, uint8_t flags, void *user_data)
{
    //printf("on_header_callback\n");
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_RESPONSE)
        {
            /* Print response headers for the initiated request. */
            //print_header(stderr, name, namelen, value, valuelen);
            onClientHeaderCallback(user_data, frame->hd.stream_id,
                                   (void *)name, namelen, (void *)value, valuelen);
            break;
        }
    }
    return 0;
}

static int on_client_begin_headers_callback(nghttp2_session *session,
                                            const nghttp2_frame *frame,
                                            void *user_data)
{
    //printf("on_begin_headers_callback\n");
    int stream_id = frame->hd.stream_id;
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_RESPONSE)
        {
            //fprintf(stderr, "Response headers for stream ID=%d:\n",
            //        frame->hd.stream_id);
            onClientBeginHeaderCallback(user_data, stream_id);
        }
        break;
    }
    return 0;
}

int on_invalid_frame_recv_callback(nghttp2_session *session,
                                   const nghttp2_frame *frame, int lib_error_code, void *user_data)
{
    printf("on_invalid_frame_recv, frame %d, code %d, msg %s\n",
           frame->hd.type,
           lib_error_code,
           nghttp2_strerror(lib_error_code));
    return 0;
}

static int on_client_frame_send_callback(nghttp2_session *session,
                                         const nghttp2_frame *frame, void *user_data)
{
    size_t i;
    (void)user_data;
    //printf("on_frame_send_callback\n");
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        /*
        if (nghttp2_session_get_stream_user_data(session, frame->hd.stream_id))
        {
            */
        if (1)
        {
            const nghttp2_nv *nva = frame->headers.nva;
            printf("[INFO] C ----------------------------> S (HEADERS)\n");
            for (i = 0; i < frame->headers.nvlen; ++i)
            {
                fwrite(nva[i].name, 1, nva[i].namelen, stdout);
                printf(": ");
                fwrite(nva[i].value, 1, nva[i].valuelen, stdout);
                printf("\n");
            }
        }
        break;
    case NGHTTP2_RST_STREAM:
        printf("[INFO] C ----------------------------> S (RST_STREAM)\n");
        break;
    case NGHTTP2_GOAWAY:
        printf("[INFO] C ----------------------------> S (GOAWAY)\n");
        break;
    }
    return 0;
}

static int on_client_frame_recv_callback(nghttp2_session *session,
                                         const nghttp2_frame *frame, void *user_data)
{
    //printf("on_frame_recv_callback %d\n", frame->hd.type);
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_RESPONSE)
        {
            //fprintf(stderr, "All headers received\n");
            onClientHeadersDoneCallback(user_data, frame->hd.stream_id);
        }
        break;
    case NGHTTP2_RST_STREAM:
        printf("server send rst_stream %d\n", frame->rst_stream.error_code);
        break;
    case NGHTTP2_GOAWAY:
        printf("server send go away\n");
        break;
    }
    return 0;
}

static int on_client_data_chunk_recv_callback(nghttp2_session *session, uint8_t flags,
                                              int32_t stream_id, const uint8_t *data,
                                              size_t len, void *user_data)
{
    return onClientDataChunkRecv(user_data, stream_id, (void *)data, len);
}

static int on_client_stream_close_callback(nghttp2_session *session, int32_t stream_id,
                                           uint32_t error_code, void *user_data)
{
    onClientStreamClose(user_data, stream_id);
    return 0;
}

static ssize_t data_source_read_callback(nghttp2_session *session, int32_t stream_id,
                                         uint8_t *buf, size_t length, uint32_t *data_flags,
                                         nghttp2_data_source *source, void *user_data)
{
    int ret = onDataSourceReadCallback(source->ptr, buf, length);
    if (ret == 0)
    {
        *data_flags = NGHTTP2_DATA_FLAG_EOF;
    }
    return ret;
}
int on_error_callback(nghttp2_session *session, int lib_error_code,
                      const char *msg, size_t len, void *user_data)
{
    //printf("errmsg %*s\n", msg, len);
    printf("code: %d, error: %s\n", lib_error_code, nghttp2_strerror(lib_error_code));
    return 0;
}

void init_client_callbacks(nghttp2_session_callbacks *callbacks)
{
    nghttp2_session_callbacks_set_send_callback(callbacks, client_send_callback);
    nghttp2_session_callbacks_set_recv_callback(callbacks, client_recv_callback);

    //nghttp2_session_callbacks_set_error_callback2(callbacks, on_error_callback);
    nghttp2_session_callbacks_set_on_invalid_frame_recv_callback(callbacks, on_invalid_frame_recv_callback);
    nghttp2_session_callbacks_set_on_frame_recv_callback(callbacks,
                                                         on_client_frame_recv_callback);

    //nghttp2_session_callbacks_set_on_frame_send_callback(callbacks, on_client_frame_send_callback);
    nghttp2_session_callbacks_set_on_data_chunk_recv_callback(
        callbacks, on_client_data_chunk_recv_callback);

    nghttp2_session_callbacks_set_on_stream_close_callback(
        callbacks, on_client_stream_close_callback);

    nghttp2_session_callbacks_set_on_header_callback(callbacks,
                                                     on_client_header_callback);

    nghttp2_session_callbacks_set_on_begin_headers_callback(
        callbacks, on_client_begin_headers_callback);
}

nghttp2_session *init_nghttp2_client_session(size_t data)
{
    int ret;
    nghttp2_session *session;
    nghttp2_session_callbacks *callbacks;
    nghttp2_session_callbacks_new(&callbacks);
    init_client_callbacks(callbacks);
    ret = nghttp2_session_client_new(&session, callbacks, (void *)((int *)(data)));
    if (session == NULL)
    {
        printf("c init session failed: %s\n", nghttp2_strerror(ret));
    }
    return session;
}

int send_client_connection_header(nghttp2_session *session)
{
    nghttp2_settings_entry iv[1] = {
        {NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS, 100}};
    int rv;

    /* client 24 bytes magic string will be sent by nghttp2 library */
    /*
    rv = nghttp2_submit_settings(session, NGHTTP2_FLAG_NONE, iv,
                                 ARRLEN(iv));
                                 */

    rv = nghttp2_submit_settings(session, NGHTTP2_FLAG_NONE, NULL, 0);
    /*
    if (rv != 0)
    {
        errx(1, "Could not submit SETTINGS: %s", nghttp2_strerror(rv));
    }
    */
    return rv;
}

int32_t submit_request(nghttp2_session *session, nghttp2_nv *hdrs, size_t hdrlen,
                       nghttp2_data_provider *dp)
{
    int32_t stream_id;
    /*
    nghttp2_nv hdrs[] = {
        MAKE_NV2(":method", "GET"),
        MAKE_NV(":scheme", &uri[u->field_data[UF_SCHEMA].off],
                u->field_data[UF_SCHEMA].len),
        MAKE_NV(":authority", stream_data->authority, stream_data->authoritylen),
        MAKE_NV(":path", stream_data->path, stream_data->pathlen)};
    fprintf(stderr, "Request headers:\n");
    print_headers(stderr, hdrs, ARRLEN(hdrs));
    */
    /*
    int i;
    for (i = 0; i < hdrlen; i++)
    {
        printf("header %s: %s\n", hdrs[i].name, hdrs[i].value);
    }
    */
    stream_id = nghttp2_submit_request(session, NULL, hdrs,
                                       hdrlen, dp, NULL);
    /*
    if (stream_id < 0)
    {
        errx(1, "Could not submit HTTP request: %s", nghttp2_strerror(stream_id));
    }
    */

    return stream_id;
}

struct nv_array *new_nv_array(size_t n)
{
    struct nv_array *a = malloc(sizeof(struct nv_array));
    nghttp2_nv *nv = (nghttp2_nv *)malloc(n * sizeof(nghttp2_nv));
    memset(nv, 0, n * sizeof(nghttp2_nv));
    a->nv = nv;
    a->len = n;
    return a;
}

int nv_array_set(struct nv_array *a, int index,
                 char *name, char *value,
                 size_t namelen, size_t valuelen, int flag)
{
    if (index > (a->len - 1))
    {
        return -1;
    }
    nghttp2_nv *nv = &((a->nv)[index]);
    nv->name = name;
    nv->value = value;
    nv->namelen = namelen;
    nv->valuelen = valuelen;
    nv->flags = flag;
    return 0;
}

void delete_nv_array(struct nv_array *a)
{
    int i;
    nghttp2_nv *nv;
    for (i = 0; i < a->len; i++)
    {
        nv = &((a->nv)[i]);
        if (nv->name != NULL)
        {
            free(nv->name);
        }
        if (nv->value != NULL)
        {
            free(nv->value);
        }
    }
    free(a->nv);
    free(a);
}

nghttp2_data_provider *new_data_provider(size_t data)
{
    nghttp2_data_provider *dp = malloc(sizeof(nghttp2_data_provider));
    dp->source.ptr = (void *)((int *)data);
    dp->read_callback = data_source_read_callback;
    return dp;
}