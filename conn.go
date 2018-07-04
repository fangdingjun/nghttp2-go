package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var (
	errAgain = errors.New("again")
)

// ClientConn http2 client connection
type ClientConn struct {
	session *C.nghttp2_session
	conn    net.Conn
	streams map[int]*ClientStream
	lock    *sync.Mutex
	errch   chan struct{}
	exitch  chan struct{}
	err     error
}

// NewClientConn create http2 client
func NewClientConn(c net.Conn) (*ClientConn, error) {
	conn := &ClientConn{
		conn: c, streams: make(map[int]*ClientStream),
		lock:   new(sync.Mutex),
		errch:  make(chan struct{}),
		exitch: make(chan struct{}),
	}
	conn.session = C.init_client_session(
		C.size_t(int(uintptr(unsafe.Pointer(conn)))))
	if conn.session == nil {
		return nil, fmt.Errorf("init session failed")
	}
	ret := C.send_client_connection_header(conn.session)
	if int(ret) < 0 {
		log.Printf("submit settings error: %s",
			C.GoString(C.nghttp2_strerror(ret)))
	}
	go conn.run()
	return conn, nil
}

func (c *ClientConn) onDataRecv(buf []byte, streamID int) {
	s := c.streams[streamID]
	if s.res.Body == nil {
		log.Println("empty body")
		return
	}

	if bp, ok := s.res.Body.(*bodyProvider); ok {
		bp.Write(buf)
	}
}

func (c *ClientConn) onBeginHeader(streamID int) {
	s := c.streams[streamID]

	s.res = &http.Response{
		Header: make(http.Header),
		Body: &bodyProvider{
			buf:  new(bytes.Buffer),
			lock: new(sync.Mutex),
		},
	}
}

func (c *ClientConn) onHeader(streamID int, name, value string) {
	s := c.streams[streamID]
	if name == ":status" {
		statusCode, _ := strconv.Atoi(value)
		s.res.StatusCode = statusCode
		s.res.Status = http.StatusText(statusCode)
		s.res.Proto = "HTTP/2.0"
		s.res.ProtoMajor = 2
		s.res.ProtoMinor = 0
		return
	}
	s.res.Header.Add(name, value)

}

func (c *ClientConn) onHeadersDone(streamID int) {
	s := c.streams[streamID]
	s.resch <- s.res
}

func (c *ClientConn) onStreamClose(streamID int) {
	stream, ok := c.streams[streamID]
	if ok {
		stream.Close()
		c.lock.Lock()
		delete(c.streams, streamID)
		c.lock.Unlock()
	}

}

// Close close the http2 connection
func (c *ClientConn) Close() error {
	for _, s := range c.streams {
		s.Close()
	}
	C.nghttp2_session_terminate_session(c.session, 0)
	C.nghttp2_session_del(c.session)
	close(c.exitch)
	c.conn.Close()
	return nil
}

func (c *ClientConn) run() {
	var wantRead int
	var wantWrite int
	var delay = 50
	var ret C.int

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

// CreateRequest submit a request and return a http.Response,
func (c *ClientConn) CreateRequest(req *http.Request) (*http.Response, error) {
	if c.err != nil {
		return nil, c.err
	}

	nvIndex := 0
	nvMax := 25
	nva := C.new_nv_array(C.size_t(nvMax))
	setNvArray(nva, nvIndex, ":method", req.Method, 0)
	nvIndex++
	setNvArray(nva, nvIndex, ":scheme", "https", 0)
	nvIndex++
	setNvArray(nva, nvIndex, ":authority", req.Host, 0)
	nvIndex++

	p := req.URL.Path
	q := req.URL.Query().Encode()
	if q != "" {
		p = p + "?" + q
	}
	setNvArray(nva, nvIndex, ":path", p, 0)
	nvIndex++
	for k, v := range req.Header {
		if strings.ToLower(k) == "host" {
			continue
		}
		//log.Printf("header %s: %s", k, v)
		setNvArray(nva, nvIndex, strings.Title(k), v[0], 0)
		nvIndex++
	}
	var dp *dataProvider
	var cdp *C.nghttp2_data_provider
	if req.Body != nil {
		dp, cdp = newDataProvider()
		go func() {
			io.Copy(dp, req.Body)
			dp.Close()
		}()
	}
	streamID := C.submit_request(c.session, nva.nv, C.size_t(nvIndex), cdp)
	if dp != nil {
		dp.streamID = int(streamID)
		dp.session = c.session
	}
	C.delete_nv_array(nva)
	if int(streamID) < 0 {
		return nil, fmt.Errorf("submit request error: %s",
			C.GoString(C.nghttp2_strerror(streamID)))
	}
	//log.Println("stream id ", int(streamID))
	s := &ClientStream{
		streamID: int(streamID),
		dp:       dp,
		cdp:      cdp,
		resch:    make(chan *http.Response),
		errch:    make(chan error),
	}
	c.lock.Lock()
	c.streams[int(streamID)] = s
	c.lock.Unlock()

	select {
	case err := <-s.errch:
		return nil, err
	case res := <-s.resch:
		return res, nil
	case <-c.errch:
		return nil, fmt.Errorf("connection error")
	}
	//return nil, fmt.Errorf("unknown error")
}

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

// HTTP2Handler is the http2 server handler that can co-work with standard net/http.
//
// usage example:
//  l, err := net.Listen("tcp", ":1222")
//  srv := &http.Server{
//    TLSConfig: &tls.Config{
//        NextProtos:[]string{"h2", "http/1.1"},
//   }
//   TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){
//       "h2": nghttp2.Http2Handler
//     }
//   }
//  srv.ServeTLS(l, "server.crt", "server.key")
func HTTP2Handler(srv *http.Server, conn *tls.Conn, handler http.Handler) {
	h2conn, err := NewServerConn(conn, handler)
	if err != nil {
		panic(err.Error())
	}
	h2conn.Run()
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
