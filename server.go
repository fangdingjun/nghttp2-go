package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// ServerConn server connection
type ServerConn struct {
	// Handler handler to handle request
	Handler http.Handler

	session *C.nghttp2_session
	streams map[int]*ServerStream
	lock    *sync.Mutex
	conn    net.Conn
	errch   chan struct{}
	exitch  chan struct{}
	err     error
}

// ServerStream server stream
type ServerStream struct {
	streamID int
	// headers receive done
	headersDone bool
	// is stream_end flag received
	streamEnd bool
	// request
	req *http.Request
	// response header
	header http.Header
	// response statusCode
	statusCode int
	// response has send
	responseSend bool

	// server connection
	conn *ServerConn

	// data provider
	dp  *dataProvider
	cdp *C.nghttp2_data_provider

	closed bool
	//buf    *bytes.Buffer
}

type bodyProvider struct {
	buf    *bytes.Buffer
	closed bool
	lock   *sync.Mutex
}

func (bp *bodyProvider) Read(buf []byte) (int, error) {
	for {
		bp.lock.Lock()
		n, err := bp.buf.Read(buf)
		bp.lock.Unlock()
		if err != nil && !bp.closed {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return n, err
	}
}

func (bp *bodyProvider) Write(buf []byte) (int, error) {
	bp.lock.Lock()
	defer bp.lock.Unlock()
	return bp.buf.Write(buf)
}

func (bp *bodyProvider) Close() error {
	/*
		if c, ok := bp.w.(io.Closer); ok{
			return c.Close()
		}
	*/
	bp.closed = true
	return nil
}

// NewServerConn create new server connection
func NewServerConn(c net.Conn, handler http.Handler) (*ServerConn, error) {
	conn := &ServerConn{
		conn:    c,
		Handler: handler,
		streams: make(map[int]*ServerStream),
		lock:    new(sync.Mutex),
		errch:   make(chan struct{}),
		exitch:  make(chan struct{}),
	}
	conn.session = C.init_server_session(C.size_t(uintptr(unsafe.Pointer(conn))))
	if conn.session == nil {
		return nil, fmt.Errorf("init session failed")
	}
	//log.Println("send server connection header")
	ret := C.send_server_connection_header(conn.session)
	if int(ret) < 0 {
		log.Println(C.GoString(C.nghttp2_strerror(ret)))
		return nil, fmt.Errorf("send connection header failed")
	}
	//go conn.run()
	return conn, nil
}

func (c *ServerConn) serve(s *ServerStream) {
	var handler = c.Handler
	if c.Handler == nil {
		handler = http.DefaultServeMux
	}
	handler.ServeHTTP(s, s.req)
	s.Close()
}

// Close close the server connection
func (c *ServerConn) Close() error {
	for _, s := range c.streams {
		s.Close()
	}
	C.nghttp2_session_terminate_session(c.session, 0)
	C.nghttp2_session_del(c.session)
	close(c.exitch)
	c.conn.Close()
	return nil
}

// Run run the server loop
func (c *ServerConn) Run() {
	var wantRead int
	var wantWrite int
	var delay = 50
	var ret C.int

	defer c.Close()
	defer close(c.errch)

	datach := make(chan []byte)
	errch := make(chan error)

	go func() {
		buf := make([]byte, 16*1024)
	readloop:
		for {
			select {
			case <-c.exitch:
				break readloop
			default:
			}

			n, err := c.conn.Read(buf)
			if err != nil {
				errch <- err
				break
			}
			datach <- buf[:n]
		}
	}()

loop:
	for {
		select {
		case <-c.errch:
			break loop
		case err := <-errch:
			c.err = err
			break loop
		case <-c.exitch:
			break loop
		default:
		}

		wantWrite = int(C.nghttp2_session_want_write(c.session))
		if wantWrite != 0 {
			ret = C.nghttp2_session_send(c.session)
			if int(ret) < 0 {
				c.err = fmt.Errorf("sesion send error: %s",
					C.GoString(C.nghttp2_strerror(ret)))
				log.Println(c.err)
				break
			}
		}

		wantRead = int(C.nghttp2_session_want_read(c.session))
		select {
		case d := <-datach:
			d1 := C.CBytes(d)
			ret1 := C.nghttp2_session_mem_recv(c.session,
				(*C.uchar)(d1), C.size_t(int(len(d))))
			C.free(d1)
			if int(ret1) < 0 {
				c.err = fmt.Errorf("sesion recv error: %s",
					C.GoString(C.nghttp2_strerror(ret)))
				log.Println(c.err)
				break loop
			}
		default:
		}

		// make delay when no data read/write
		if wantRead == 0 && wantWrite == 0 {
			select {
			case <-time.After(time.Duration(delay) * time.Millisecond):
			}
		}
	}
}

// Write implements http.ResponseWriter
func (s *ServerStream) Write(buf []byte) (int, error) {
	if !s.responseSend {
		s.WriteHeader(http.StatusOK)
	}
	/*
		//log.Printf("stream %d, send %d bytes", s.streamID, len(buf))
		if s.buf.Len() > 2048 {
			s.dp.Write(s.buf.Bytes())
			s.buf.Reset()
		}

		if len(buf) < 2048 {
			s.buf.Write(buf)
			return len(buf), nil
		}
	*/
	return s.dp.Write(buf)
}

// WriteHeader implements http.ResponseWriter
func (s *ServerStream) WriteHeader(code int) {
	s.statusCode = code
	nvIndex := 0
	nvMax := 25
	nva := C.new_nv_array(C.size_t(nvMax))
	setNvArray(nva, nvIndex, ":status", fmt.Sprintf("%d", code), 0)
	nvIndex++

	for k, v := range s.header {
		if strings.ToLower(k) == "host" {
			continue
		}
		//log.Printf("header %s: %s", k, v)
		setNvArray(nva, nvIndex, strings.Title(k), v[0], 0)
		nvIndex++
	}
	var dp *dataProvider
	var cdp *C.nghttp2_data_provider
	dp, cdp = newDataProvider()
	dp.streamID = s.streamID
	dp.session = s.conn.session
	s.dp = dp
	s.cdp = cdp
	ret := C.nghttp2_submit_response(
		s.conn.session, C.int(s.streamID), nva.nv, C.size_t(nvIndex), cdp)
	C.delete_nv_array(nva)
	if int(ret) < 0 {
		panic(fmt.Sprintf("sumit response error %s", C.GoString(C.nghttp2_strerror(ret))))
	}
	s.responseSend = true
	log.Printf("stream %d send response", s.streamID)
}

// Header implements http.ResponseWriter
func (s *ServerStream) Header() http.Header {
	if s.header == nil {
		s.header = http.Header{}
	}
	return s.header
}

// Close close the stream
func (s *ServerStream) Close() error {
	if s.closed {
		return nil
	}
	//C.nghttp2_submit_rst_stream(s.conn.session, 0, C.int(s.streamID), 0)
	s.req.Body.Close()
	if s.dp != nil {
		s.dp.Close()
	}
	s.closed = true
	log.Printf("stream %d closed", s.streamID)
	return nil
}

// ServerDataRecv callback function for receive data from network
//export ServerDataRecv
func ServerDataRecv(ptr unsafe.Pointer, data unsafe.Pointer,
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

// ServerDataSend callback function for send data to network
//export ServerDataSend
func ServerDataSend(ptr unsafe.Pointer, data unsafe.Pointer,
	length C.size_t) C.ssize_t {
	//log.Println("server data send")
	conn := (*ServerConn)(ptr)
	buf := C.GoBytes(data, C.int(length))
	n, err := conn.conn.Write(buf)
	if err != nil {
		return -1
	}
	//log.Println("send ", n, " bytes to network ", buf)
	return C.ssize_t(n)
}

// OnServerDataRecv callback function for data recv
//export OnServerDataRecv
func OnServerDataRecv(ptr unsafe.Pointer, streamID C.int,
	data unsafe.Pointer, length C.size_t) C.int {
	conn := (*ServerConn)(ptr)
	s := conn.streams[int(streamID)]
	bp := s.req.Body.(*bodyProvider)
	buf := C.GoBytes(data, C.int(length))
	bp.Write(buf)
	return C.int(length)
}

// OnServerBeginHeaderCallback callback function for begin header
//export OnServerBeginHeaderCallback
func OnServerBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*ServerConn)(ptr)
	s := &ServerStream{
		streamID: int(streamID),
		conn:     conn,
		req: &http.Request{
			URL:        &url.URL{},
			Header:     http.Header{},
			Proto:      "HTTP/2.0",
			ProtoMajor: 2,
			ProtoMinor: 0,
		},
		//buf: new(bytes.Buffer),
	}
	conn.streams[int(streamID)] = s
	return 0
}

// OnServerHeaderCallback callback function for header
//export OnServerHeaderCallback
func OnServerHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	conn := (*ServerConn)(ptr)
	s := conn.streams[int(streamID)]
	hdrname := C.GoStringN((*C.char)(name), namelen)
	hdrvalue := C.GoStringN((*C.char)(value), valuelen)
	hdrname = strings.ToLower(hdrname)
	switch hdrname {
	case ":method":
		s.req.Method = hdrvalue
	case ":scheme":
		s.req.URL.Scheme = hdrvalue
	case ":path":
		s.req.RequestURI = hdrvalue
		u, _ := url.ParseRequestURI(s.req.RequestURI)
		scheme := s.req.URL.Scheme
		*(s.req.URL) = *u
		if scheme != "" {
			s.req.URL.Scheme = scheme
		}
	case ":authority":
		s.req.Host = hdrvalue
	default:
		s.req.Header.Add(hdrname, hdrvalue)

	}
	return 0
}

// OnServerStreamEndCallback callback function for frame received
//export OnServerStreamEndCallback
func OnServerStreamEndCallback(ptr unsafe.Pointer, streamID C.int) C.int {

	conn := (*ServerConn)(ptr)
	s := conn.streams[int(streamID)]
	s.streamEnd = true
	bp := s.req.Body.(*bodyProvider)
	if s.req.Method != "CONNECT" {
		bp.closed = true
		log.Println("stream end flag set, begin to serve")
		go conn.serve(s)
	}
	return 0
}

// OnServerHeadersDoneCallback callback function for all headers received
//export OnServerHeadersDoneCallback
func OnServerHeadersDoneCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*ServerConn)(ptr)
	s := conn.streams[int(streamID)]
	s.headersDone = true
	bp := &bodyProvider{
		buf:  new(bytes.Buffer),
		lock: new(sync.Mutex),
	}
	s.req.Body = bp
	if s.req.Method == "CONNECT" {
		go conn.serve(s)
	}
	return 0
}

// OnServerStreamClose callback function for stream close
//export OnServerStreamClose
func OnServerStreamClose(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*ServerConn)(ptr)
	s := conn.streams[int(streamID)]
	conn.lock.Lock()
	delete(conn.streams, int(streamID))
	conn.lock.Unlock()
	s.Close()
	return 0
}
