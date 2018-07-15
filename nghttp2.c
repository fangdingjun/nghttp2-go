#include "_nghttp2.h"

int on_error_callback(nghttp2_session *session, const char *msg,
                      size_t len, void *user_data);

int on_invalid_frame_recv_callback(nghttp2_session *session,
                                   const nghttp2_frame *frame,
                                   int lib_error_code, void *user_data);

int init_nghttp2_callbacks(nghttp2_session_callbacks *callbacks);

int on_error_callback(nghttp2_session *session, const char *msg,
                      size_t len, void *user_data)
{
    printf("on error callback: %s\n", msg);
    return 0;
}
int on_invalid_frame_recv_callback(nghttp2_session *session,
                                   const nghttp2_frame *frame,
                                   int lib_error_code, void *user_data)
{
    printf("invalid frame recv: %s\n", nghttp2_strerror(lib_error_code));
    return 0;
}
static ssize_t on_data_source_read_callback(nghttp2_session *session, int32_t stream_id,
                                                   uint8_t *buf, size_t length, uint32_t *data_flags,
                                                   nghttp2_data_source *source, void *user_data)
{
    int ret = onDataSourceReadCallback(user_data, stream_id, buf, length);
    if (ret == 0)
    {
        *data_flags = NGHTTP2_DATA_FLAG_EOF;
    }
    return ret;
}

static ssize_t on_send_callback(nghttp2_session *session,
                                const uint8_t *data, size_t length,
                                int flags, void *user_data)
{
    return onDataSendCallback(user_data, (void *)data, length);
}

static int on_frame_recv_callback(nghttp2_session *session,
                                  const nghttp2_frame *frame,
                                  void *user_data)
{
    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_REQUEST)
        {
            onHeadersDoneCallback(user_data, frame->hd.stream_id);
        }
    case NGHTTP2_DATA:
        if (frame->hd.flags & NGHTTP2_FLAG_END_STREAM)
        {
            onStreamEndCallback(user_data, frame->hd.stream_id);
        }
        break;
    }
    return 0;
}

static int on_stream_close_callback(nghttp2_session *session,
                                    int32_t stream_id,
                                    uint32_t error_code,
                                    void *user_data)

{
    onStreamClose(user_data, stream_id);
    return 0;
}

static int on_header_callback(nghttp2_session *session,
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
            onHeaderCallback(user_data, frame->hd.stream_id,
                             (void *)name, namelen, (void *)value, valuelen);
        }
        break;
    }
    return 0;
}

static int on_data_chunk_recv_callback(nghttp2_session *session,
                                       uint8_t flags,
                                       int32_t stream_id,
                                       const uint8_t *data,
                                       size_t len, void *user_data)
{
    return onDataChunkRecv(user_data, stream_id, (void *)data, len);
}

static int on_begin_headers_callback(nghttp2_session *session,
                                     const nghttp2_frame *frame,
                                     void *user_data)
{

    switch (frame->hd.type)
    {
    case NGHTTP2_HEADERS:
        if (frame->headers.cat == NGHTTP2_HCAT_REQUEST)
        {
            onBeginHeaderCallback(user_data, frame->hd.stream_id);
        }
        break;
    }
    return 0;
}

nghttp2_session *init_nghttp2_server_session(size_t data)
{
    nghttp2_session_callbacks *callbacks;
    nghttp2_session *session;

    init_nghttp2_callbacks(callbacks);
    nghttp2_session_server_new(&session, callbacks, (void *)((int *)(data)));

    nghttp2_session_callbacks_del(callbacks);
    return session;
}

nghttp2_session *init_nghttp2_client_session(size_t data)
{
    nghttp2_session_callbacks *callbacks;
    nghttp2_session *session;
    nghttp2_session_callbacks_new(&callbacks);
    init_nghttp2_callbacks(callbacks);
    nghttp2_session_client_new(&session, callbacks, (void *)((int *)(data)));

    nghttp2_session_callbacks_del(callbacks);
    return session;
}

int init_nghttp2_callbacks(nghttp2_session_callbacks *callbacks)
{

    nghttp2_session_callbacks_set_send_callback(callbacks, on_send_callback);

    nghttp2_session_callbacks_set_on_frame_recv_callback(callbacks,
                                                         on_frame_recv_callback);

    nghttp2_session_callbacks_set_on_stream_close_callback(
        callbacks, on_stream_close_callback);

    nghttp2_session_callbacks_set_on_invalid_frame_recv_callback(callbacks,
                                                                 on_invalid_frame_recv_callback);

    nghttp2_session_callbacks_set_on_data_chunk_recv_callback(
        callbacks, on_data_chunk_recv_callback);
    nghttp2_session_callbacks_set_on_header_callback(callbacks,
                                                     on_header_callback);

    nghttp2_session_callbacks_set_error_callback(callbacks, on_error_callback);
    nghttp2_session_callbacks_set_on_begin_headers_callback(
        callbacks, on_begin_headers_callback);
}

int send_connection_header(nghttp2_session *session)
{
    nghttp2_settings_entry iv[1] = {
        {NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS, 100}};
    int rv;

    rv = nghttp2_submit_settings(session, NGHTTP2_FLAG_NONE, iv,
                                 ARRLEN(iv));
    return rv;
}

int data_provider_set_callback(size_t dp, size_t data, int t){
    nghttp2_data_provider *cdp = (nghttp2_data_provider*)dp;
    cdp->source.ptr = (void *)data;
    cdp->read_callback=on_data_source_read_callback;
}