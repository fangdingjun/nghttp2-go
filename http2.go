package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
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

// Conn http2 connection
type Conn struct {
	session *C.nghttp2_session
	conn    net.Conn
	streams map[int]*Stream
	lock    *sync.Mutex
	errch   chan struct{}
	err     error
}

// Stream http2 stream
type Stream struct {
	streamID int
	cdp      *C.nghttp2_data_provider
	dp       *dataProvider
	// application read data from stream
	r *io.PipeReader
	// recv stream data from session
	w     *io.PipeWriter
	res   *http.Response
	resch chan *http.Response
	errch chan error
}

type dataProvider struct {
	// drain the data
	r io.Reader
	// provider the data
	w io.Writer
}

// NewConn create http2 connection
func NewConn(c net.Conn) (*Conn, error) {
	conn := &Conn{
		conn: c, streams: make(map[int]*Stream), lock: new(sync.Mutex),
		errch: make(chan struct{}),
	}
	conn.session = C.init_nghttp2_session(C.size_t(int(uintptr(unsafe.Pointer(conn)))))
	if conn.session == nil {
		return nil, fmt.Errorf("init session failed")
	}
	ret := C.send_client_connection_header(conn.session)
	if int(ret) < 0 {
		log.Printf("submit settings error: %s", C.GoString(C.nghttp2_strerror(ret)))
	}
	go conn.run()
	return conn, nil
}

func (c *Conn) onDataRecv(buf []byte, streamID int) {
	stream := c.streams[streamID]
	stream.onDataRecv(buf)
}

func (c *Conn) onBeginHeader(streamID int) {
	stream := c.streams[streamID]
	stream.onBeginHeader()
}

func (c *Conn) onHeader(streamID int, name, value string) {
	stream := c.streams[streamID]
	stream.onHeader(name, value)

}

func (c *Conn) onFrameRecv(streamID int) {
	stream := c.streams[streamID]
	stream.onFrameRecv()
}

func (c *Conn) onStreamClose(streamID int) {
	stream, ok := c.streams[streamID]
	if ok {
		stream.Close()
		c.lock.Lock()
		delete(c.streams, streamID)
		c.lock.Unlock()
	}

}

// Close close the http2 connection
func (c *Conn) Close() error {
	for _, s := range c.streams {
		s.Close()
	}
	C.nghttp2_session_del(c.session)
	close(c.errch)
	c.conn.Close()
	return nil
}

func (c *Conn) run() {
	var wantRead int
	var wantWrite int
	var delay = 50
	var ret C.int

loop:
	for {
		select {
		case <-c.errch:
			break loop
		default:
		}

		wantRead = int(C.nghttp2_session_want_read(c.session))
		wantWrite = int(C.nghttp2_session_want_write(c.session))
		if wantWrite != 0 {
			ret = C.nghttp2_session_send(c.session)
			if int(ret) < 0 {
				c.err = fmt.Errorf("sesion send error: %s", C.GoString(C.nghttp2_strerror(ret)))
				log.Println(c.err)
				break
			}
		}
		if wantRead != 0 {
			ret = C.nghttp2_session_recv(c.session)
			if int(ret) < 0 {
				c.err = fmt.Errorf("sesion recv error: %s", C.GoString(C.nghttp2_strerror(ret)))
				log.Println(c.err)
				break
			}
		}

		// make delay when no data read/write
		if wantRead == 0 && wantWrite == 0 {
			select {
			case <-time.After(time.Duration(delay) * time.Millisecond):
			}
		}
	}
}

// NewRequest create a new http2 stream
func (c *Conn) NewRequest(req *http.Request) (*http.Response, error) {
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
		setNvArray(nva, nvIndex, strings.Title(k), v[0], 0)
		nvIndex++
	}
	var dp *dataProvider
	var cdp *C.nghttp2_data_provider
	if req.Body != nil {
		dp, cdp = newDataProvider(req.Body, nil)
	}
	streamID := C.submit_request(c.session, nva.nv, C.size_t(nvIndex+1))
	C.delete_nv_array(nva)
	if int(streamID) < 0 {
		return nil, fmt.Errorf("submit request error: %s", C.GoString(C.nghttp2_strerror(streamID)))
	}
	log.Println("stream id ", int(streamID))
	r, w := io.Pipe()
	s := &Stream{
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
	}
	//return nil, fmt.Errorf("unknown error")
}

func setNvArray(a *C.struct_nv_array, index int, name, value string, flags int) {
	cname := C.CString(name)
	cvalue := C.CString(value)
	cnamelen := C.size_t(len(name))
	cvaluelen := C.size_t(len(value))
	cflags := C.int(flags)
	defer C.free(unsafe.Pointer(cname))
	defer C.free(unsafe.Pointer(cvalue))
	C.nv_array_set(a, C.int(index), cname,
		cvalue, cnamelen, cvaluelen, cflags)
}

func (dp *dataProvider) Read(buf []byte) (n int, err error) {
	return dp.r.Read(buf)
}

func (dp *dataProvider) Write(buf []byte) (n int, err error) {
	if dp.w == nil {
		return 0, fmt.Errorf("write not supported")
	}
	return dp.w.Write(buf)
}

func newDataProvider(r io.Reader, w io.Writer) (*dataProvider, *C.nghttp2_data_provider) {
	dp := &dataProvider{r, w}
	cdp := C.new_data_provider(unsafe.Pointer(dp))
	return dp, cdp
}

func (s *Stream) Read(buf []byte) (n int, err error) {
	return s.r.Read(buf)
}

func (s *Stream) Write(buf []byte) (n int, err error) {
	return s.dp.Write(buf)
}

func (s *Stream) onDataRecv(buf []byte) {
	s.w.Write(buf)
}

func (s *Stream) onBeginHeader() {
	s.res = &http.Response{
		Header: make(http.Header),
	}
}

func (s *Stream) onHeader(name, value string) {
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

func (s *Stream) onFrameRecv() {
	s.res.Body = s
	s.resch <- s.res
}

// Close close the stream
func (s *Stream) Close() error {
	select {
	case s.errch <- fmt.Errorf("stream closed"):
	}
	close(s.resch)
	close(s.errch)
	s.w.Close()
	return nil
}

// DataSourceRead callback function for data read from data provider source
//export DataSourceRead
func DataSourceRead(ptr unsafe.Pointer, buf unsafe.Pointer, length C.size_t) C.ssize_t {
	log.Println("data source read")
	dp := (*dataProvider)(ptr)
	gobuf := make([]byte, int(length))
	n, err := dp.Read(gobuf)
	if err != nil {
		if err == io.EOF {
			return 0
		}
		return -1
	}
	cbuf := C.CBytes(gobuf)
	defer C.free(cbuf)
	C.memcpy(buf, cbuf, C.size_t(n))
	return C.ssize_t(n)
}

// OnDataRecv callback function for data frame received
//export OnDataRecv
func OnDataRecv(ptr unsafe.Pointer, streamID C.int, buf unsafe.Pointer, length C.size_t) C.int {
	log.Println("on data recv")
	conn := (*Conn)(ptr)
	gobuf := C.GoBytes(buf, C.int(length))
	conn.onDataRecv(gobuf, int(streamID))
	return 0
}

// DataRead callback function for session wants read data from peer
//export DataRead
func DataRead(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	log.Println("data read")
	conn := (*Conn)(ptr)
	buf := make([]byte, int(size))
	log.Println(conn.conn.RemoteAddr())
	n, err := conn.conn.Read(buf)
	if err != nil {
		log.Println(err)
		return -1
	}
	log.Println("read from network ", n)
	return C.ssize_t(n)
}

// DataWrite callback function for session wants send data to peer
//export DataWrite
func DataWrite(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	log.Println("data write")
	conn := (*Conn)(ptr)
	buf := C.GoBytes(data, C.int(size))
	log.Println(conn.conn.RemoteAddr())
	n, err := conn.conn.Write(buf)
	if err != nil {
		log.Println(err)
		return -1
	}
	log.Println("write data to network ", n)
	return C.ssize_t(n)
}

// OnBeginHeaderCallback callback function for response
//export OnBeginHeaderCallback
func OnBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	log.Println("begin header")
	conn := (*Conn)(ptr)
	conn.onBeginHeader(int(streamID))
	return 0
}

// OnHeaderCallback callback function for header
//export OnHeaderCallback
func OnHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	log.Println("header")
	conn := (*Conn)(ptr)
	goname := C.GoBytes(name, namelen)
	govalue := C.GoBytes(value, valuelen)
	conn.onHeader(int(streamID), string(goname), string(govalue))
	return 0
}

// OnFrameRecvCallback callback function for begion to recv data
//export OnFrameRecvCallback
func OnFrameRecvCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	log.Println("frame recv")
	conn := (*Conn)(ptr)
	conn.onFrameRecv(int(streamID))
	return 0
}

// OnStreamClose callback function for stream close
//export OnStreamClose
func OnStreamClose(ptr unsafe.Pointer, streamID C.int) C.int {
	log.Println("stream close")
	conn := (*Conn)(ptr)
	conn.onStreamClose(int(streamID))
	return 0
}
