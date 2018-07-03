package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
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

// ClientConn http2 connection
type ClientConn struct {
	session  *C.nghttp2_session
	conn     net.Conn
	streams  map[int]*ClientStream
	lock     *sync.Mutex
	errch    chan struct{}
	exitch   chan struct{}
	err      error
	isServer bool
}

// ClientStream http2 stream
type ClientStream struct {
	streamID int
	cdp      *C.nghttp2_data_provider
	dp       *dataProvider
	// application read data from stream
	r *io.PipeReader
	// recv stream data from session
	w      *io.PipeWriter
	res    *http.Response
	resch  chan *http.Response
	errch  chan error
	closed bool
}

type dataProvider struct {
	// drain the data
	r io.Reader
	// provider the data
	w        io.Writer
	datach   chan []byte
	errch    chan error
	buf      *bytes.Buffer
	run      bool
	streamID int
	session  *C.nghttp2_session
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
	stream := c.streams[streamID]
	stream.onDataRecv(buf)
}

func (c *ClientConn) onBeginHeader(streamID int) {
	stream := c.streams[streamID]
	stream.onBeginHeader()
}

func (c *ClientConn) onHeader(streamID int, name, value string) {
	stream := c.streams[streamID]
	stream.onHeader(name, value)

}

func (c *ClientConn) onFrameRecv(streamID int) {
	stream := c.streams[streamID]
	stream.onFrameRecv()
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

// CreateRequest submit a request and return a http.Response, client only
func (c *ClientConn) CreateRequest(req *http.Request) (*http.Response, error) {
	if c.err != nil {
		return nil, c.err
	}

	if c.isServer {
		return nil, fmt.Errorf("only client can create new request")
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
		dp, cdp = newDataProvider(req.Body, nil)
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
	r, w := io.Pipe()
	s := &ClientStream{
		streamID: int(streamID),
		dp:       dp,
		cdp:      cdp,
		r:        r,
		w:        w,
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

func setNvArray(a *C.struct_nv_array, index int,
	name, value string, flags int) {
	cname := C.CString(name)
	cvalue := C.CString(value)
	cnamelen := C.size_t(len(name))
	cvaluelen := C.size_t(len(value))
	cflags := C.int(flags)
	//defer C.free(unsafe.Pointer(cname))
	//defer C.free(unsafe.Pointer(cvalue))
	C.nv_array_set(a, C.int(index), cname,
		cvalue, cnamelen, cvaluelen, cflags)
}

func (dp *dataProvider) start() {
	buf := make([]byte, 4096)
	for {
		n, err := dp.r.Read(buf)
		if err != nil {
			dp.errch <- err
			break
		}
		dp.datach <- buf[:n]
		C.nghttp2_session_resume_data(dp.session, C.int(dp.streamID))
	}
}

// Read read from data provider
// this emulate a unblocking reading
// if data is not avaliable return errAgain
func (dp *dataProvider) Read(buf []byte) (n int, err error) {
	if !dp.run {
		go dp.start()
		dp.run = true
		time.Sleep(100 * time.Millisecond)
	}

	select {
	case err := <-dp.errch:
		//log.Println("d err ", err)
		return 0, err
	case b := <-dp.datach:
		dp.buf.Write(b)
	default:
	}
	n, err = dp.buf.Read(buf)
	if err != nil {
		//log.Println(err)
		return 0, errAgain
	}
	return
}

// Write provider data for data provider
func (dp *dataProvider) Write(buf []byte) (n int, err error) {
	if dp.w == nil {
		return 0, fmt.Errorf("write not supported")
	}
	return dp.w.Write(buf)
}

func newDataProvider(r io.Reader, w io.Writer) (
	*dataProvider, *C.nghttp2_data_provider) {
	dp := &dataProvider{
		r: r, w: w,
		errch:  make(chan error),
		datach: make(chan []byte),
		buf:    new(bytes.Buffer),
	}
	cdp := C.new_data_provider(C.size_t(uintptr(unsafe.Pointer(dp))))
	return dp, cdp
}

func (s *ClientStream) Read(buf []byte) (n int, err error) {
	return s.r.Read(buf)
}

func (s *ClientStream) Write(buf []byte) (n int, err error) {
	return s.dp.Write(buf)
}

func (s *ClientStream) onDataRecv(buf []byte) {
	s.w.Write(buf)
}

func (s *ClientStream) onBeginHeader() {
	s.res = &http.Response{
		Header: make(http.Header),
	}
}

func (s *ClientStream) onHeader(name, value string) {
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

func (s *ClientStream) onFrameRecv() {
	s.res.Body = s
	s.resch <- s.res
	//log.Println("stream frame recv")
}

// Close close the stream
func (s *ClientStream) Close() error {
	if s.closed {
		return nil
	}
	err := fmt.Errorf("stream closed")
	//log.Println("close stream")
	select {
	case s.errch <- err:
	default:
	}
	//log.Println("close stream resch")
	close(s.resch)
	//log.Println("close stream errch")
	close(s.errch)
	//log.Println("close pipe w")
	s.w.CloseWithError(err)
	//log.Println("close stream done")
	s.closed = true
	return nil
}

// DataSourceRead callback function for data read from data provider source
//export DataSourceRead
func DataSourceRead(ptr unsafe.Pointer,
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
			// NGHTTP2_ERR_DEFERED
			return -508
		}
		return -1
	}
	cbuf := C.CBytes(gobuf)
	defer C.free(cbuf)
	C.memcpy(buf, cbuf, C.size_t(n))
	return C.ssize_t(n)
}

// OnClientDataRecv callback function for data frame received
//export OnClientDataRecv
func OnClientDataRecv(ptr unsafe.Pointer, streamID C.int,
	buf unsafe.Pointer, length C.size_t) C.int {
	//log.Println("on data recv")
	conn := (*ClientConn)(ptr)
	gobuf := C.GoBytes(buf, C.int(length))
	conn.onDataRecv(gobuf, int(streamID))
	return 0
}

// ClientDataRecv callback function for session wants read data from peer
//export ClientDataRecv
func ClientDataRecv(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
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

// ClientDataSend callback function for session wants send data to peer
//export ClientDataSend
func ClientDataSend(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	//log.Println("data write req ", int(size))
	conn := (*ClientConn)(ptr)
	buf := C.GoBytes(data, C.int(size))
	//log.Println(conn.conn.RemoteAddr())
	n, err := conn.conn.Write(buf)
	if err != nil {
		//log.Println(err)
		return -1
	}
	//log.Println("write data to network ", n)
	return C.ssize_t(n)
}

// OnClientBeginHeaderCallback callback function for response
//export OnClientBeginHeaderCallback
func OnClientBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("begin header")
	conn := (*ClientConn)(ptr)
	conn.onBeginHeader(int(streamID))
	return 0
}

// OnClientHeaderCallback callback function for header
//export OnClientHeaderCallback
func OnClientHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	//log.Println("header")
	conn := (*ClientConn)(ptr)
	goname := C.GoBytes(name, namelen)
	govalue := C.GoBytes(value, valuelen)
	conn.onHeader(int(streamID), string(goname), string(govalue))
	return 0
}

// OnClientHeadersDoneCallback callback function for begion to recv data
//export OnClientHeadersDoneCallback
func OnClientHeadersDoneCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("frame recv")
	conn := (*ClientConn)(ptr)
	conn.onFrameRecv(int(streamID))
	return 0
}

// OnClientStreamClose callback function for stream close
//export OnClientStreamClose
func OnClientStreamClose(ptr unsafe.Pointer, streamID C.int) C.int {
	//log.Println("stream close")
	conn := (*ClientConn)(ptr)
	conn.onStreamClose(int(streamID))
	return 0
}
