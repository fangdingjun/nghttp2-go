package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	session     *C.nghttp2_session
	conn        net.Conn
	streams     map[int]*ClientStream
	lock        *sync.Mutex
	errch       chan struct{}
	exitch      chan struct{}
	err         error
	closed      bool
	streamCount int
}

// Client create http2 client
func Client(c net.Conn) (*ClientConn, error) {
	conn := &ClientConn{
		conn: c, streams: make(map[int]*ClientStream),
		lock:   new(sync.Mutex),
		errch:  make(chan struct{}),
		exitch: make(chan struct{}),
	}
	conn.session = C.init_nghttp2_client_session(
		C.size_t(int(uintptr(unsafe.Pointer(conn)))))
	if conn.session == nil {
		return nil, fmt.Errorf("init session failed")
	}
	ret := C.nghttp2_submit_settings(conn.session, 0, nil, 0)
	if int(ret) < 0 {
		conn.Close()
		return nil, fmt.Errorf("submit settings error: %s",
			C.GoString(C.nghttp2_strerror(ret)))
	}
	go conn.run()
	return conn, nil
}

// Error return current error on connection
func (c *ClientConn) Error() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.err
}

// Close close the http2 connection
func (c *ClientConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	for _, s := range c.streams {
		s.Close()
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	//log.Println("close client connection")
	C.nghttp2_session_terminate_session(c.session, 0)
	C.nghttp2_session_del(c.session)
	close(c.exitch)
	c.conn.Close()
	return nil
}

func (c *ClientConn) run() {
	var wantWrite int
	var delay = 50 * time.Millisecond
	var keepalive = 5 * time.Second
	var ret C.int
	var lastDataRecv time.Time

	//defer c.Close()

	defer close(c.errch)

	errch := make(chan struct{}, 5)

	// data read loop
	go func() {
		buf := make([]byte, 16*1024)
	readloop:
		for {
			select {
			case <-c.exitch:
				break readloop
			case <-errch:
				break readloop
			default:
			}

			n, err := c.conn.Read(buf)
			if err != nil {
				c.lock.Lock()
				c.err = err
				c.lock.Unlock()
				close(errch)
				break
			}
			//log.Printf("read %d bytes from network", n)
			lastDataRecv = time.Now()
			d1 := C.CBytes(buf[:n])

			c.lock.Lock()
			ret1 := C.nghttp2_session_mem_recv(c.session,
				(*C.uchar)(d1), C.size_t(n))
			c.lock.Unlock()

			C.free(d1)
			if int(ret1) < 0 {
				c.lock.Lock()
				c.err = fmt.Errorf("sesion recv error: %s",
					C.GoString(C.nghttp2_strerror(ret)))
				//log.Println(c.err)
				c.lock.Unlock()
				close(errch)
				break
			}
		}
	}()

	// keep alive loop
	go func() {
		for {
			select {
			case <-c.exitch:
				return
			case <-errch:
				return
			case <-time.After(keepalive):
			}
			now := time.Now()
			last := lastDataRecv
			d := now.Sub(last)
			if d > keepalive {
				c.lock.Lock()
				C.nghttp2_submit_ping(c.session, 0, nil)
				c.lock.Unlock()
				//log.Println("submit ping")
			}
		}
	}()

loop:
	for {
		select {
		case <-c.errch:
			break loop
		case <-errch:
			break loop
		case <-c.exitch:
			break loop
		default:
		}

		c.lock.Lock()
		ret = C.nghttp2_session_send(c.session)
		c.lock.Unlock()

		if int(ret) < 0 {
			c.lock.Lock()
			c.err = fmt.Errorf("sesion send error: %s",
				C.GoString(C.nghttp2_strerror(ret)))
			c.lock.Unlock()
			//log.Println(c.err)
			close(errch)
			break
		}

		c.lock.Lock()
		wantWrite = int(C.nghttp2_session_want_write(c.session))
		c.lock.Unlock()

		// make delay when no data read/write
		if wantWrite == 0 {
			time.Sleep(delay)
		}
	}
}

// Connect submit a CONNECT request, return a ClientStream and http status code from server
//
// equals to "CONNECT host:port" in http/1.1
func (c *ClientConn) Connect(addr string) (cs *ClientStream, statusCode int, err error) {
	if c.err != nil {
		return nil, http.StatusServiceUnavailable, c.err
	}
	nvIndex := 0
	nvMax := 5
	nva := C.new_nv_array(C.size_t(nvMax))
	//log.Printf("%s %s", req.Method, req.RequestURI)
	setNvArray(nva, nvIndex, ":method", "CONNECT", 0)
	nvIndex++
	//setNvArray(nva, nvIndex, ":scheme", "https", 0)
	//nvIndex++
	//log.Printf("header authority: %s", req.RequestURI)
	setNvArray(nva, nvIndex, ":authority", addr, 0)
	nvIndex++

	var dp *dataProvider
	var cdp *C.nghttp2_data_provider
	dp, cdp = newDataProvider(c.lock)

	c.lock.Lock()
	streamID := C.nghttp2_submit_request(c.session, nil,
		nva.nv, C.size_t(nvIndex), cdp, nil)
	c.lock.Unlock()

	C.delete_nv_array(nva)
	if int(streamID) < 0 {
		return nil, http.StatusServiceUnavailable, fmt.Errorf(
			"submit request error: %s", C.GoString(C.nghttp2_strerror(streamID)))
	}
	if dp != nil {
		dp.streamID = int(streamID)
		dp.session = c.session
	}
	//log.Println("stream id ", int(streamID))
	s := &ClientStream{
		streamID: int(streamID),
		conn:     c,
		dp:       dp,
		cdp:      cdp,
		resch:    make(chan *http.Response),
		errch:    make(chan error),
		lock:     new(sync.Mutex),
	}

	c.lock.Lock()
	c.streams[int(streamID)] = s
	c.streamCount++
	c.lock.Unlock()

	//log.Printf("new stream id %d", int(streamID))
	select {
	case err := <-s.errch:
		//log.Println("wait response, got ", err)
		return nil, http.StatusServiceUnavailable, err
	case res := <-s.resch:
		if res != nil {
			return s, res.StatusCode, nil
		}
		//log.Println("wait response, empty response")
		return nil, http.StatusServiceUnavailable, io.EOF
	case <-c.errch:
		return nil, http.StatusServiceUnavailable, fmt.Errorf("connection error")
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
	if req.Method != "CONNECT" {
		setNvArray(nva, nvIndex, ":scheme", "https", 0)
		nvIndex++
	}
	setNvArray(nva, nvIndex, ":authority", req.Host, 0)
	nvIndex++

	/*
		:path must starts with "/"
		req.RequestURI maybe starts with http://
	*/
	p := req.URL.Path
	q := req.URL.Query().Encode()
	if q != "" {
		p = p + "?" + q
	}

	if req.Method != "CONNECT" {
		setNvArray(nva, nvIndex, ":path", p, 0)
		nvIndex++
	}

	//log.Printf("%s http://%s%s", req.Method, req.Host, p)
	for k, v := range req.Header {
		//log.Printf("header %s: %s\n", k, v[0])
		_k := strings.ToLower(k)
		if _k == "host" || _k == "connection" || _k == "proxy-connection" {
			continue
		}
		//log.Printf("header %s: %s", k, v)
		setNvArray(nva, nvIndex, strings.Title(k), v[0], 0)
		nvIndex++
	}

	var dp *dataProvider
	var cdp *C.nghttp2_data_provider

	if req.Method == "PUT" || req.Method == "POST" || req.Method == "CONNECT" {
		dp, cdp = newDataProvider(c.lock)
		go func() {
			io.Copy(dp, req.Body)
			dp.Close()
		}()
	}

	c.lock.Lock()
	streamID := C.nghttp2_submit_request(c.session, nil,
		nva.nv, C.size_t(nvIndex), cdp, nil)
	c.lock.Unlock()

	C.delete_nv_array(nva)

	if int(streamID) < 0 {
		return nil, fmt.Errorf("submit request error: %s",
			C.GoString(C.nghttp2_strerror(streamID)))
	}

	//log.Printf("new stream, id %d", int(streamID))

	if dp != nil {
		dp.streamID = int(streamID)
		dp.session = c.session
	}

	s := &ClientStream{
		streamID: int(streamID),
		conn:     c,
		dp:       dp,
		cdp:      cdp,
		resch:    make(chan *http.Response),
		errch:    make(chan error),
		lock:     new(sync.Mutex),
	}

	c.lock.Lock()
	c.streams[int(streamID)] = s
	c.streamCount++
	c.lock.Unlock()

	// waiting for response from server
	select {
	case err := <-s.errch:
		return nil, err
	case res := <-s.resch:
		if res != nil {
			res.Request = req
			return res, nil
		}
		return nil, io.EOF
	case <-c.errch:
		return nil, fmt.Errorf("connection error")
	}
	//return nil, fmt.Errorf("unknown error")
}

// CanTakeNewRequest check if the ClientConn can submit a new request
func (c *ClientConn) CanTakeNewRequest() bool {
	c.lock.Lock()
	c.lock.Unlock()

	if c.closed {
		return false
	}

	if c.err != nil {
		return false
	}

	if c.streamCount > ((1 << 31) / 2) {
		return false
	}
	return true
}

// ServerConn server connection
type ServerConn struct {
	// Handler handler to handle request
	Handler http.Handler

	closed  bool
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
	h2conn, err := Server(conn, handler)
	if err != nil {
		panic(err.Error())
	}
	h2conn.Run()
}

// Server create new server connection
func Server(c net.Conn, handler http.Handler) (*ServerConn, error) {
	conn := &ServerConn{
		conn:    c,
		Handler: handler,
		streams: make(map[int]*ServerStream),
		lock:    new(sync.Mutex),
		errch:   make(chan struct{}),
		exitch:  make(chan struct{}),
	}

	conn.session = C.init_nghttp2_server_session(
		C.size_t(uintptr(unsafe.Pointer(conn))))
	if conn.session == nil {
		return nil, fmt.Errorf("init session failed")
	}

	//log.Println("send server connection header")
	ret := C.send_server_connection_header(conn.session)
	if int(ret) < 0 {
		return nil, fmt.Errorf(C.GoString(C.nghttp2_strerror(ret)))
		//return nil, fmt.Errorf("send connection header failed")
	}
	//go conn.run()
	return conn, nil
}

func (c *ServerConn) serve(s *ServerStream) {
	var handler = c.Handler
	if c.Handler == nil {
		handler = http.DefaultServeMux
	}

	if s.req.URL == nil {
		s.req.URL = &url.URL{}
	}

	// call http.Handler to serve request
	handler.ServeHTTP(s, s.req)
	s.Close()
}

// Close close the server connection
func (c *ServerConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	for _, s := range c.streams {
		s.Close()
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	C.nghttp2_session_terminate_session(c.session, 0)
	C.nghttp2_session_del(c.session)
	close(c.exitch)
	c.conn.Close()

	return nil
}

// Run run the server loop
func (c *ServerConn) Run() {
	var wantWrite int
	var delay = 100 * time.Millisecond
	var ret C.int

	defer c.Close()
	defer close(c.errch)

	errch := make(chan struct{}, 5)

	go func() {
		buf := make([]byte, 16*1024)
	readloop:
		for {
			select {
			case <-c.exitch:
				break readloop
			case <-errch:
				break readloop
			default:
			}

			n, err := c.conn.Read(buf)
			if err != nil {
				c.lock.Lock()
				c.err = err
				c.lock.Unlock()
				close(errch)
				break
			}

			d1 := C.CBytes(buf[:n])

			c.lock.Lock()
			ret1 := C.nghttp2_session_mem_recv(c.session,
				(*C.uchar)(d1), C.size_t(n))
			c.lock.Unlock()

			C.free(d1)
			if int(ret1) < 0 {
				c.lock.Lock()
				c.err = fmt.Errorf("sesion recv error: %s",
					C.GoString(C.nghttp2_strerror(ret)))
				c.lock.Unlock()
				//log.Println(c.err)
				close(errch)
				break
			}
		}
	}()

loop:
	for {
		select {
		case <-c.errch:
			break loop
		case <-errch:
			break loop
		case <-c.exitch:
			break loop
		default:
		}

		c.lock.Lock()
		ret = C.nghttp2_session_send(c.session)
		c.lock.Unlock()

		if int(ret) < 0 {
			c.lock.Lock()
			c.err = fmt.Errorf("sesion send error: %s",
				C.GoString(C.nghttp2_strerror(ret)))
			c.lock.Unlock()
			//log.Println(c.err)
			close(errch)
			break
		}

		c.lock.Lock()
		wantWrite = int(C.nghttp2_session_want_write(c.session))
		c.lock.Unlock()

		// make delay when no data read/write
		if wantWrite == 0 {
			time.Sleep(delay)
		}
	}
}
