#include "_nghttp2.h"

static ssize_t server_send_callback(nghttp2_session *session,
                                    const uint8_t *data, size_t length,
                                    int flags, void *user_data)
{
    return ServerDataSend(user_data, (void *)data, length);
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
            OnServerHeadersDoneCallback(user_data, frame->hd.stream_id);
        }
    case NGHTTP2_DATA:
        if (frame->hd.flags & NGHTTP2_FLAG_END_STREAM)
        {
            OnServerStreamEndCallback(user_data, frame->hd.stream_id);
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
    OnServerStreamClose(user_data, stream_id);
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
            OnServerHeaderCallback(user_data, frame->hd.stream_id,
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
    return OnServerDataRecv(user_data, stream_id, (void *)data, len);
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
            OnServerBeginHeaderCallback(user_data, frame->hd.stream_id);
        }
        break;
    }
    return 0;
}

nghttp2_session *init_server_session(size_t data)
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