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
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Conn http2 connection
type Conn struct {
	conn        net.Conn
	session     *C.nghttp2_session
	streams     map[int]*stream
	streamCount int
	closed      bool
	isServer    bool
	running     bool
	handler     http.Handler
	lock        *sync.Mutex
	err         error
	errch       chan error
	exitch      chan struct{}
}

// Dial connect to addr and create a http2 client Conn
//
// the Conn.Run have already called, should not call it again
func Dial(network, addr string, cfg *tls.Config) (*Conn, error) {
	nextProto := []string{"h2"}
	if cfg == nil {
		_addr := addr
		h, _, err := net.SplitHostPort(addr)
		if err == nil {
			_addr = h
		}
		cfg = &tls.Config{ServerName: _addr}
	}
	cfg.NextProtos = nextProto
	conn, err := tls.Dial(network, addr, cfg)
	if err != nil {
		return nil, err
	}
	if err := conn.Handshake(); err != nil {
		return nil, err
	}
	state := conn.ConnectionState()
	if state.NegotiatedProtocol != "h2" {
		return nil, errors.New("server not support http2")
	}
	return Client(conn)
}

// Server create server side http2 connection on c
//
// c must be TLS connection and negotiated for h2
//
// the Conn.Run not called, you must run it
func Server(c net.Conn, handler http.Handler) (*Conn, error) {
	conn := &Conn{
		conn:     c,
		handler:  handler,
		errch:    make(chan error),
		exitch:   make(chan struct{}),
		lock:     new(sync.Mutex),
		isServer: true,
		streams:  make(map[int]*stream),
	}
	//log.Printf("new conn %x", uintptr(unsafe.Pointer(conn)))
	runtime.SetFinalizer(conn, (*Conn).free)
	conn.session = C.init_nghttp2_server_session(C.size_t(uintptr(unsafe.Pointer(conn))))
	if conn.session == nil {
		return nil, errors.New("init server session failed")
	}
	ret := C.send_connection_header(conn.session)
	if int(ret) < 0 {
		conn.Close()
		return nil, fmt.Errorf("send settings error: %s", C.GoString(C.nghttp2_strerror(ret)))
	}
	return conn, nil
}

// Client create client side http2 connection on c
//
// c must be TLS connection and negotiated for h2
//
// the Conn.Run have alread called, you should not call it again
func Client(c net.Conn) (*Conn, error) {
	conn := &Conn{
		conn:    c,
		errch:   make(chan error),
		exitch:  make(chan struct{}),
		lock:    new(sync.Mutex),
		streams: make(map[int]*stream),
	}
	//log.Printf("new conn %x", uintptr(unsafe.Pointer(conn)))
	runtime.SetFinalizer(conn, (*Conn).free)
	conn.session = C.init_nghttp2_client_session(C.size_t(uintptr(unsafe.Pointer(conn))))
	if conn.session == nil {
		return nil, errors.New("init server session failed")
	}
	ret := C.send_connection_header(conn.session)
	if int(ret) < 0 {
		conn.Close()
		return nil, fmt.Errorf("send settings error: %s", C.GoString(C.nghttp2_strerror(ret)))
	}
	go conn.Run()
	return conn, nil
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

func (c *Conn) free() {
	//log.Printf("free conn %x", uintptr(unsafe.Pointer(c)))
	if !c.closed {
		c.Close()
	}
	c.conn = nil
	c.session = nil
	c.streams = nil
	c.lock = nil
}

// Error return conn error
func (c *Conn) Error() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.err
}

// CanTakeNewRequest check if conn can create new request
func (c *Conn) CanTakeNewRequest() bool {
	if c.streamCount > ((2<<31 - 1) / 2) {
		return false
	}
	if c.err != nil {
		return false
	}
	return true
}

// RoundTrip submit http request and return the response
func (c *Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	nv := []C.nghttp2_nv{}

	nv = append(nv, newNV(":method", req.Method))
	nv = append(nv, newNV(":authority", req.Host))
	nv = append(nv, newNV(":scheme", "https"))

	p := req.URL.Path
	q := req.URL.Query().Encode()
	if q != "" {
		p = fmt.Sprintf("%s?%s", p, q)
	}
	nv = append(nv, newNV(":path", p))
	for k, v := range req.Header {
		_k := strings.ToLower(k)
		if _k == "connection" || _k == "proxy-connection" || _k == "transfer-encoding" {
			continue
		}
		nv = append(nv, newNV(k, v[0]))
	}
	cdp := C.nghttp2_data_provider{}
	dp := newDataProvider(unsafe.Pointer(&cdp), c.lock, 1)
	dp.session = c.session
	var _cdp *C.nghttp2_data_provider
	if req.Method == "POST" || req.Method == "PUT" {
		_cdp = &cdp
	}
	s, err := c.submitRequest(nv, _cdp)
	if err != nil {
		return nil, err
	}
	s.dp = dp
	s.dp.streamID = s.streamID

	c.lock.Lock()
	c.streams[s.streamID] = s
	c.streamCount++
	c.lock.Unlock()
	if req.Method == "POST" || req.Method == "PUT" {
		go func() {
			io.Copy(dp, req.Body)
			dp.Close()
		}()
	}
	select {
	case res := <-s.resch:
		/*
			if res.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("http error code %d", res.StatusCode)
			}
		*/
		s.request = req
		res.Request = s.request
		return res, nil
	case <-c.exitch:
		return nil, errors.New("connection closed")
	}
}

func (c *Conn) submitRequest(nv []C.nghttp2_nv, cdp *C.nghttp2_data_provider) (*stream, error) {

	c.lock.Lock()
	ret := C._nghttp2_submit_request(c.session, nil,
		C.size_t(uintptr(unsafe.Pointer(&nv[0]))), C.size_t(len(nv)), cdp, nil)
	c.lock.Unlock()

	if int(ret) < 0 {
		return nil, fmt.Errorf("submit request error: %s", C.GoString(C.nghttp2_strerror(ret)))
	}
	streamID := int(ret)
	s := &stream{
		streamID: streamID,
		conn:     c,
		bp: &bodyProvider{
			buf:  new(bytes.Buffer),
			lock: new(sync.Mutex),
		},
		resch: make(chan *http.Response),
	}
	if cdp != nil {
		s.cdp = *cdp
	}
	runtime.SetFinalizer(s, (*stream).free)
	return s, nil
}

// Connect submit connect request
//
// like "CONNECT host:port" on http/1.1
//
// statusCode is the http status code the server returned
//
// c bounds to the remote host of addr
func (c *Conn) Connect(addr string) (conn net.Conn, statusCode int, err error) {
	nv := []C.nghttp2_nv{}

	nv = append(nv, newNV(":method", "CONNECT"))
	nv = append(nv, newNV(":authority", addr))

	cdp := C.nghttp2_data_provider{}
	dp := newDataProvider(unsafe.Pointer(&cdp), c.lock, 1)
	dp.session = c.session

	s, err := c.submitRequest(nv, &cdp)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	s.dp = dp
	c.lock.Lock()
	c.streams[s.streamID] = s
	c.streamCount++
	c.lock.Unlock()

	s.dp.streamID = s.streamID

	select {
	case res := <-s.resch:
		if res.StatusCode != http.StatusOK {
			return nil, res.StatusCode, fmt.Errorf("http error code %d", res.StatusCode)
		}
		s.request = &http.Request{
			Method:     "CONNECT",
			Host:       addr,
			RequestURI: addr,
			URL:        &url.URL{},
		}
		res.Request = s.request
		return s, res.StatusCode, nil
	case <-c.exitch:
		return nil, http.StatusServiceUnavailable, errors.New("connection closed")
	}

}

// Run run the event loop
func (c *Conn) Run() {
	if c.running {
		return
	}
	c.running = true
	defer c.Close()

	go c.readloop()
	go c.writeloop()

	for {
		select {
		case err := <-c.errch:
			c.err = err
			return
		case <-c.exitch:
			return
		}
	}
}

func (c *Conn) serve(s *stream) {
	var handler = c.handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	s.request.RemoteAddr = c.conn.RemoteAddr().String()
	if s.request.URL == nil {
		s.request.URL = &url.URL{}
	}
	handler.ServeHTTP(s, s.request)
	s.Close()
}

// Close close the connection
func (c *Conn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	for _, s := range c.streams {
		s.Close()
	}

	c.lock.Lock()

	for n := range c.streams {
		delete(c.streams, n)
	}

	C.nghttp2_session_terminate_session(c.session, 0)
	C.nghttp2_session_del(c.session)
	c.lock.Unlock()

	close(c.exitch)
	c.conn.Close()
	return nil
}

func (c *Conn) errorNotify(err error) {
	select {
	case c.errch <- err:
	default:
	}
}

func (c *Conn) readloop() {
	buf := make([]byte, 16*1024)
	for {
		select {
		case <-c.exitch:
			return
		default:
		}

		n, err := c.conn.Read(buf)
		if err != nil {
			c.errorNotify(err)
			return
		}

		c.lock.Lock()
		if c.closed {
			c.lock.Unlock()
			return
		}

		ret := C.nghttp2_session_mem_recv(c.session,
			(*C.uchar)(unsafe.Pointer(&buf[0])), C.size_t(n))
		c.lock.Unlock()
		if int(ret) < 0 {
			err = fmt.Errorf("http2 recv error: %s", C.GoString(C.nghttp2_strerror(C.int(ret))))
			c.errorNotify(err)
			return
		}
	}
}

func (c *Conn) writeloop() {
	var ret C.int
	var err error
	var delay = 50 * time.Millisecond

	for {
		select {
		case <-c.exitch:
			return
		default:
		}
		c.lock.Lock()
		ret = C.nghttp2_session_send(c.session)
		c.lock.Unlock()
		if int(ret) < 0 {
			err = fmt.Errorf("http2 send error: %s", C.GoString(C.nghttp2_strerror(C.int(ret))))
			c.errorNotify(err)
			return
		}
		c.lock.Lock()
		wantWrite := C.nghttp2_session_want_write(c.session)
		c.lock.Unlock()
		if int(wantWrite) == 0 {
			//log.Println("write loop, sleep")
			time.Sleep(delay)
		}
	}
}
