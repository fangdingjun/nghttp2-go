package nghttp2

/*
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

const (
	NGHTTP2_NO_ERROR                      = 0
	NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE = -521
	NGHTTP2_ERR_CALLBACK_FAILURE          = -902
	NGHTTP2_ERR_DEFERRED                  = -508
)

// OnServerDataRecvCallback callback function for libnghttp2 library
// want receive data from network.
//
//export OnServerDataRecvCallback
func OnServerDataRecvCallback(ptr unsafe.Pointer, data unsafe.Pointer,
	length C.size_t) C.ssize_t {
	conn := (*ServerConn)(ptr)
	buf := make([]byte, int(length))
	n, err := conn.conn.Read(buf)
	if err != nil {
		return -1
	}
	cbuf := C.CBytes(buf[:n])
	defer C.free(cbuf)
	C.memcpy(data, cbuf, C.size_t(n))
	return C.ssize_t(n)
}

// OnServerDataSendCallback callback function for libnghttp2 library
// want send data to network.
//
//export OnServerDataSendCallback
func OnServerDataSendCallback(ptr unsafe.Pointer, data unsafe.Pointer,
	length C.size_t) C.ssize_t {
	//log.Println("server data send")
	conn := (*ServerConn)(ptr)
	buf := C.GoBytes(data, C.int(length))
	n, err := conn.conn.Write(buf)
	if err != nil {
		return NGHTTP2_ERR_CALLBACK_FAILURE
	}
	//log.Println("send ", n, " bytes to network ", buf)
	return C.ssize_t(n)
}

// OnServerDataChunkRecv callback function for libnghttp2 library's data chunk recv.
//
//export OnServerDataChunkRecv
func OnServerDataChunkRecv(ptr unsafe.Pointer, streamID C.int,
	data unsafe.Pointer, length C.size_t) C.int {
	conn := (*ServerConn)(ptr)
	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	bp := s.req.Body.(*bodyProvider)
	buf := C.GoBytes(data, C.int(length))
	bp.Write(buf)
	return C.int(length)
}

// OnServerBeginHeaderCallback callback function for begin begin header recv.
//
//export OnServerBeginHeaderCallback
func OnServerBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*ServerConn)(ptr)
	var TLS tls.ConnectionState
	if tlsconn, ok := conn.conn.(*tls.Conn); ok {
		TLS = tlsconn.ConnectionState()
	}

	s := &ServerStream{
		streamID: int(streamID),
		conn:     conn,
		req: &http.Request{
			//URL:        &url.URL{},
			Header:     http.Header{},
			Proto:      "HTTP/2.0",
			ProtoMajor: 2,
			ProtoMinor: 0,
			RemoteAddr: conn.conn.RemoteAddr().String(),
			TLS:        &TLS,
		},
		//buf: new(bytes.Buffer),
	}
	conn.streams[int(streamID)] = s
	return NGHTTP2_NO_ERROR
}

// OnServerHeaderCallback callback function for each header recv.
//
//export OnServerHeaderCallback
func OnServerHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	conn := (*ServerConn)(ptr)
	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	hdrname := C.GoStringN((*C.char)(name), namelen)
	hdrvalue := C.GoStringN((*C.char)(value), valuelen)
	hdrname = strings.ToLower(hdrname)
	switch hdrname {
	case ":method":
		s.req.Method = hdrvalue
	case ":scheme":
		// s.req.URL.Scheme = hdrvalue
	case ":path":
		s.req.RequestURI = hdrvalue
		u, _ := url.ParseRequestURI(s.req.RequestURI)
		s.req.URL = u
	case ":authority":
		s.req.Host = hdrvalue
	case "content-length":
		s.req.Header.Add(hdrname, hdrvalue)
		n, err := strconv.ParseInt(hdrvalue, 10, 64)
		if err == nil {
			s.req.ContentLength = n
		}
	default:
		s.req.Header.Add(hdrname, hdrvalue)
	}
	return NGHTTP2_NO_ERROR
}

// OnServerStreamEndCallback callback function for the stream when END_STREAM flag set
//
//export OnServerStreamEndCallback
func OnServerStreamEndCallback(ptr unsafe.Pointer, streamID C.int) C.int {

	conn := (*ServerConn)(ptr)
	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}

	s.streamEnd = true
	bp := s.req.Body.(*bodyProvider)
	if s.req.Method != "CONNECT" {
		bp.closed = true
		//log.Println("stream end flag set, begin to serve")
		go conn.serve(s)
	}
	return NGHTTP2_NO_ERROR
}

// OnServerHeadersDoneCallback callback function for the stream when all headers received.
//
//export OnServerHeadersDoneCallback
func OnServerHeadersDoneCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*ServerConn)(ptr)
	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	s.headersDone = true
	bp := &bodyProvider{
		buf:  new(bytes.Buffer),
		lock: new(sync.Mutex),
	}
	s.req.Body = bp
	if s.req.Method == "CONNECT" {
		go conn.serve(s)
	}
	return NGHTTP2_NO_ERROR
}

// OnServerStreamClose callback function for the stream when closed.
//
//export OnServerStreamClose
func OnServerStreamClose(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*ServerConn)(ptr)
	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	conn.lock.Lock()
	delete(conn.streams, int(streamID))
	conn.lock.Unlock()
	s.Close()
	return NGHTTP2_NO_ERROR
}

// OnDataSourceReadCallback callback function for libnghttp2 library
// want read data from data provider source,
// return NGHTTP2_ERR_DEFERRED will cause data frame defered,
// application later call nghttp2_session_resume_data will re-quene the data frame
//
//export OnDataSourceReadCallback
func OnDataSourceReadCallback(ptr unsafe.Pointer,
	buf unsafe.Pointer, length C.size_t) C.ssize_t {
	//log.Println("data source read")
	dp := (*dataProvider)(ptr)
	gobuf := make([]byte, int(length))
	n, err := dp.Read(gobuf)
	if err != nil {
		if err == io.EOF {
			return 0
		}
		if err == errAgain {
			return NGHTTP2_ERR_DEFERRED
		}
		return NGHTTP2_ERR_CALLBACK_FAILURE
	}
	cbuf := C.CBytes(gobuf)
	defer C.free(cbuf)
	C.memcpy(buf, cbuf, C.size_t(n))
	return C.ssize_t(n)
}

// OnClientDataChunkRecv callback function for libnghttp2 library data chunk received.
//
//export OnClientDataChunkRecv
func OnClientDataChunkRecv(ptr unsafe.Pointer, streamID C.int,
	buf unsafe.Pointer, length C.size_t) C.int {
	//log.Println("on data recv")
	conn := (*ClientConn)(ptr)
	gobuf := C.GoBytes(buf, C.int(length))

	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	if s.res.Body == nil {
		//log.Println("empty body")
		return C.int(length)
	}

	if bp, ok := s.res.Body.(*bodyProvider); ok {
		n, err := bp.Write(gobuf)
		if err != nil {
			return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
		}
		return C.int(n)
	}
	return C.int(length)
}

// OnClientDataRecvCallback callback function for libnghttp2 library want read data from network.
//
//export OnClientDataRecvCallback
func OnClientDataRecvCallback(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	//log.Println("data read req", int(size))
	conn := (*ClientConn)(ptr)
	buf := make([]byte, int(size))
	//log.Println(conn.conn.RemoteAddr())
	n, err := conn.conn.Read(buf)
	if err != nil {
		//log.Println(err)
		return -1
	}
	cbuf := C.CBytes(buf)
	//log.Println("read from network ", n, buf[:n])
	C.memcpy(data, cbuf, C.size_t(n))
	return C.ssize_t(n)
}

// OnClientDataSendCallback callback function for libnghttp2 library want send data to network.
//
//export OnClientDataSendCallback
func OnClientDataSendCallback(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	//log.Println("data write req ", int(size))
	conn := (*ClientConn)(ptr)
	buf := C.GoBytes(data, C.int(size))
	//log.Println(conn.conn.RemoteAddr())
	n, err := conn.conn.Write(buf)
	if err != nil {
		return NGHTTP2_ERR_CALLBACK_FAILURE
	}
	//log.Println("write data to network ", n)
	return C.ssize_t(n)
}

// OnClientBeginHeaderCallback callback function for begin header receive.
//
//export OnClientBeginHeaderCallback
func OnClientBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("begin header")
	conn := (*ClientConn)(ptr)

	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	var TLS tls.ConnectionState
	if tlsconn, ok := conn.conn.(*tls.Conn); ok {
		TLS = tlsconn.ConnectionState()
	}
	s.res = &http.Response{
		Header: make(http.Header),
		Body: &bodyProvider{
			buf:  new(bytes.Buffer),
			lock: new(sync.Mutex),
		},
		TLS: &TLS,
	}
	return NGHTTP2_NO_ERROR
}

// OnClientHeaderCallback callback function for each header received.
//
//export OnClientHeaderCallback
func OnClientHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	//log.Println("header")
	conn := (*ClientConn)(ptr)
	goname := string(C.GoBytes(name, namelen))
	govalue := string(C.GoBytes(value, valuelen))

	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	goname = strings.ToLower(goname)
	switch goname {
	case ":status":
		statusCode, _ := strconv.Atoi(govalue)
		s.res.StatusCode = statusCode
		s.res.Status = http.StatusText(statusCode)
		s.res.Proto = "HTTP/2.0"
		s.res.ProtoMajor = 2
		s.res.ProtoMinor = 0
	case "content-length":
		s.res.Header.Add(goname, govalue)
		n, err := strconv.ParseInt(govalue, 10, 64)
		if err == nil {
			s.res.ContentLength = n
		}
	case "transfer-encoding":
		s.res.Header.Add(goname, govalue)
		s.res.TransferEncoding = append(s.res.TransferEncoding, govalue)
	default:
		s.res.Header.Add(goname, govalue)
	}
	return NGHTTP2_NO_ERROR
}

// OnClientHeadersDoneCallback callback function for the stream when all headers received.
//
//export OnClientHeadersDoneCallback
func OnClientHeadersDoneCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("frame recv")
	conn := (*ClientConn)(ptr)
	s, ok := conn.streams[int(streamID)]
	if !ok {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	s.resch <- s.res
	return NGHTTP2_NO_ERROR
}

// OnClientStreamClose callback function for the stream when closed.
//
//export OnClientStreamClose
func OnClientStreamClose(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("stream close")
	conn := (*ClientConn)(ptr)

	stream, ok := conn.streams[int(streamID)]
	if ok {
		stream.Close()
		conn.lock.Lock()
		delete(conn.streams, int(streamID))
		conn.lock.Unlock()
		return NGHTTP2_NO_ERROR
	}
	return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
}
