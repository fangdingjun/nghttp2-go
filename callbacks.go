package nghttp2

/*
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

var (
	errAgain = errors.New("again")
)

const (
	NGHTTP2_NO_ERROR                      = 0
	NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE = -521
	NGHTTP2_ERR_CALLBACK_FAILURE          = -902
	NGHTTP2_ERR_DEFERRED                  = -508
)

var bufPool = &sync.Pool{
	New: func() interface{} {
		return make([]byte, 16*1024)
	},
}

// onDataSourceReadCallback callback function for libnghttp2 library
// want read data from data provider source,
// return NGHTTP2_ERR_DEFERRED will cause data frame defered,
// application later call nghttp2_session_resume_data will re-quene the data frame
//
//export onDataSourceReadCallback
func onDataSourceReadCallback(ptr unsafe.Pointer, streamID C.int,
	buf unsafe.Pointer, length C.size_t) C.ssize_t {
	//log.Println("onDataSourceReadCallback begin")
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	s, ok := conn.streams[int(streamID)]
	if !ok {
		//log.Println("client dp callback, stream not exists")
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	//gobuf := make([]byte, int(length))
	_length := int(length)
	gobuf := bufPool.Get().([]byte)
	if len(gobuf) < _length {
		gobuf = make([]byte, _length)
	}
	defer bufPool.Put(gobuf)

	n, err := s.dp.Read(gobuf[:_length])
	if err != nil {
		if err == io.EOF {
			//log.Println("onDataSourceReadCallback end")
			return 0
		}
		if err == errAgain {
			//log.Println("onDataSourceReadCallback end")
			//s.dp.deferred = true
			return NGHTTP2_ERR_DEFERRED
		}
		//log.Println("onDataSourceReadCallback end")
		return NGHTTP2_ERR_CALLBACK_FAILURE
	}
	//cbuf := C.CBytes(gobuf)
	//defer C.free(cbuf)
	//C.memcpy(buf, cbuf, C.size_t(n))
	C.memcpy(buf, unsafe.Pointer(&gobuf[0]), C.size_t(n))
	//log.Println("onDataSourceReadCallback end")
	return C.ssize_t(n)
}

// onDataChunkRecv callback function for libnghttp2 library data chunk received.
//
//export onDataChunkRecv
func onDataChunkRecv(ptr unsafe.Pointer, streamID C.int,
	buf unsafe.Pointer, length C.size_t) C.int {
	//log.Println("onDataChunkRecv begin")
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	gobuf := C.GoBytes(buf, C.int(length))

	s, ok := conn.streams[int(streamID)]
	if !ok {
		//log.Println("onDataChunkRecv end")
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	if s.bp == nil {
		//log.Println("empty body")
		//log.Println("onDataChunkRecv end")
		return C.int(length)
	}
	//log.Println("bp write")
	n, err := s.bp.Write(gobuf)
	if err != nil {
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	//log.Println("onDataChunkRecv end")
	return C.int(n)
}

// onDataSendCallback callback function for libnghttp2 library want send data to network.
//
//export onDataSendCallback
func onDataSendCallback(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	//log.Println("onDataSendCallback begin")
	//log.Println("data write req ", int(size))
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	buf := C.GoBytes(data, C.int(size))
	//log.Println(conn.conn.RemoteAddr())
	n, err := conn.conn.Write(buf)
	if err != nil {
		//log.Println("onDataSendCallback end")
		return NGHTTP2_ERR_CALLBACK_FAILURE
	}
	//log.Printf("write %d bytes to network ", n)
	//log.Println("onDataSendCallback end")
	return C.ssize_t(n)
}

// onBeginHeaderCallback callback function for begin header receive.
//
//export onBeginHeaderCallback
func onBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("onBeginHeaderCallback begin")
	//log.Printf("stream %d begin headers", int(streamID))
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))

	var TLS tls.ConnectionState
	if tlsconn, ok := conn.conn.(*tls.Conn); ok {
		TLS = tlsconn.ConnectionState()
	}
	// client
	if !conn.isServer {
		s, ok := conn.streams[int(streamID)]
		if !ok {
			//log.Println("onBeginHeaderCallback end")
			return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
		}
		s.response = &http.Response{
			Proto:      "HTTP/2",
			ProtoMajor: 2,
			ProtoMinor: 0,
			Header:     make(http.Header),
			Body:       s.bp,
			TLS:        &TLS,
		}
		return NGHTTP2_NO_ERROR
	}

	// server
	s := &stream{
		streamID: int(streamID),
		conn:     conn,
		bp: &bodyProvider{
			buf:  new(bytes.Buffer),
			lock: new(sync.Mutex),
		},
		request: &http.Request{
			Header:     make(http.Header),
			Proto:      "HTTP/2",
			ProtoMajor: 2,
			ProtoMinor: 0,
			TLS:        &TLS,
		},
	}
	s.request.Body = s.bp
	//log.Printf("new stream %d", int(streamID))
	conn.streams[int(streamID)] = s

	runtime.SetFinalizer(s, (*stream).free)

	//log.Println("onBeginHeaderCallback end")
	return NGHTTP2_NO_ERROR
}

// onHeaderCallback callback function for each header received.
//
//export onHeaderCallback
func onHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	//log.Println("onHeaderCallback begin")
	//log.Printf("header %d", int(streamID))
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	goname := string(C.GoBytes(name, namelen))
	govalue := string(C.GoBytes(value, valuelen))

	s, ok := conn.streams[int(streamID)]
	if !ok {
		//log.Println("onHeaderCallback end")
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	var header http.Header
	if conn.isServer {
		header = s.request.Header
	} else {
		header = s.response.Header
	}
	goname = strings.ToLower(goname)
	switch goname {
	case ":method":
		s.request.Method = govalue
	case ":scheme":
	case ":authority":
		s.request.Host = govalue
	case ":path":
		s.request.RequestURI = govalue
		u, err := url.Parse(govalue)
		if err != nil {
			return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
		}
		s.request.URL = u
	case ":status":
		if s.response == nil {
			//log.Println("empty response")
			return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
		}
		statusCode, _ := strconv.Atoi(govalue)
		s.response.StatusCode = statusCode
		s.response.Status = http.StatusText(statusCode)
	case "content-length":
		header.Add(goname, govalue)
		n, err := strconv.ParseInt(govalue, 10, 64)
		if err == nil {
			if conn.isServer {
				s.request.ContentLength = n
			} else {
				s.response.ContentLength = n
			}
		}
	case "transfer-encoding":
		header.Add(goname, govalue)
		if conn.isServer {
			s.request.TransferEncoding = append(s.response.TransferEncoding, govalue)
		} else {
			s.response.TransferEncoding = append(s.response.TransferEncoding, govalue)
		}
	default:
		header.Add(goname, govalue)
	}
	//log.Println("onHeaderCallback end")
	return NGHTTP2_NO_ERROR
}

// onHeadersDoneCallback callback function for the stream when all headers received.
//
//export onHeadersDoneCallback
func onHeadersDoneCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("onHeadersDoneCallback begin")
	//log.Printf("stream %d headers done", int(streamID))
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	s, ok := conn.streams[int(streamID)]
	if !ok {
		//log.Println("onHeadersDoneCallback end")
		return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
	}
	s.headersEnd = true
	if conn.isServer {
		if s.request.Method == "CONNECT" {
			go conn.serve(s)
		}
		return NGHTTP2_NO_ERROR
	}
	select {
	case s.resch <- s.response:
	default:
	}
	//log.Println("onHeadersDoneCallback end")
	return NGHTTP2_NO_ERROR
}

// onStreamClose callback function for the stream when closed.
//
//export onStreamClose
func onStreamClose(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("onStreamClose begin")
	//log.Printf("stream %d closed", int(streamID))
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))

	stream, ok := conn.streams[int(streamID)]
	if ok {
		go stream.Close()
		//log.Printf("remove stream %d", int(streamID))
		//conn.lock.Lock()
		delete(conn.streams, int(streamID))
		//go stream.Close()
		//conn.lock.Unlock()
		//log.Println("onStreamClose end")
		return NGHTTP2_NO_ERROR
	}
	//log.Println("onStreamClose end")
	return NGHTTP2_ERR_TEMPORAL_CALLBACK_FAILURE
}

//export onConnectionCloseCallback
func onConnectionCloseCallback(ptr unsafe.Pointer) {
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	conn.err = io.EOF
	conn.Close()
}

//export onStreamEndCallback
func onStreamEndCallback(ptr unsafe.Pointer, streamID C.int) {
	conn := (*Conn)(unsafe.Pointer(uintptr(ptr)))
	stream, ok := conn.streams[int(streamID)]
	if !ok {
		return
	}
	stream.streamEnd = true

	stream.bp.Close()

	if stream.conn.isServer {
		if stream.request.Method != "CONNECT" {
			go conn.serve(stream)
		}
		return
	}
}
