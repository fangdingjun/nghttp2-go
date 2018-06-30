package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"unsafe"
)

// Conn http2 connection
type Conn struct {
	session *C.nghttp2_session
	conn    net.Conn
	streams map[int]*Stream
}

// Stream http2 stream
type Stream struct {
	streamID int
	cdp      *C.nghttp2_data_provider
	dp       *dataProvider
	// application read data from stream
	r io.Reader
	// recv stream data from session
	w io.Writer
}

type dataProvider struct {
	// drain the data
	r io.Reader
	// provider the data
	w io.Writer
}

// NewConn create http2 connection
func NewConn(c net.Conn) *Conn {
	conn := &Conn{conn: c, streams: make(map[int]*Stream)}
	C.init_nghttp2_session(conn.session, unsafe.Pointer(conn))
	C.send_client_connection_header(conn.session)
	return conn
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

// NewRequest create a new http2 stream
func (c *Conn) NewRequest(req *http.Request) *http.Response {
	nvIndex := 0
	nvMax := 25
	nva := C.new_nv_array(C.size_t(nvMax))
	setNvArray(nva, nvIndex, ":method", req.Method, 0)
	nvIndex += 1
	setNvArray(nva, nvIndex, ":scheme", "https", 0)
	nvIndex += 1
	setNvArray(nva, nvIndex, ":authority", req.Host, 0)
	nvIndex += 1

	p := req.URL.Path
	q := req.URL.Query().Encode()
	if q != "" {
		p = p + "?" + q
	}
	setNvArray(nva, nvIndex, ":path", p, 0)
	nvIndex += 1
	for k, v := range req.Header {
		if strings.ToLower(k) == "host" {
			continue
		}
		setNvArray(nva, nvIndex, strings.Title(k), v[0], 0)
		nvIndex += 1
	}
	var dp *dataProvider
	var cdp *C.nghttp2_data_provider
	if req.Body != nil {
		dp, cdp = newDataProvider(req.Body, nil)
	}
	streamID := C.submit_request(c.session, nva.nv, C.size_t(nvIndex+1))
	C.delete_nv_array(nva)
	if int(streamID) < 0 {
		return nil
	}
	r, w := io.Pipe()
	s := &Stream{streamID: int(streamID), dp: dp, cdp: cdp, r: r, w: w}
	c.streams[int(streamID)] = s

	return nil
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

}

func (s *Stream) onHeader(name, value string) {

}

func (s *Stream) onFrameRecv() {

}

//export DataSourceRead
func DataSourceRead(ptr unsafe.Pointer, buf unsafe.Pointer, length C.size_t) C.ssize_t {
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

//export OnDataRecv
func OnDataRecv(ptr unsafe.Pointer, streamID C.int, buf unsafe.Pointer, length C.size_t) C.int {
	conn := (*Conn)(ptr)
	gobuf := C.GoBytes(buf, C.int(length))
	conn.onDataRecv(gobuf, int(streamID))
	return 0
}

//export DataRead
func DataRead(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	conn := (*Conn)(ptr)
	buf := make([]byte, int(size))
	n, err := conn.conn.Read(buf)
	if err != nil {
		return -1
	}
	return C.ssize_t(n)
}

//export DataWrite
func DataWrite(ptr unsafe.Pointer, data unsafe.Pointer, size C.size_t) C.ssize_t {
	conn := (*Conn)(ptr)
	buf := C.GoBytes(data, C.int(size))
	n, err := conn.conn.Write(buf)
	if err != nil {
		return -1
	}
	return C.ssize_t(n)
}

//export OnBeginHeaderCallback
func OnBeginHeaderCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*Conn)(ptr)
	conn.onBeginHeader(int(streamID))
	return 0
}

//export OnHeaderCallback
func OnHeaderCallback(ptr unsafe.Pointer, streamID C.int,
	name unsafe.Pointer, namelen C.int,
	value unsafe.Pointer, valuelen C.int) C.int {
	conn := (*Conn)(ptr)
	goname := C.GoBytes(name, namelen)
	govalue := C.GoBytes(value, valuelen)
	conn.onHeader(int(streamID), string(goname), string(govalue))
	return 0
}

//export OnFrameRecvCallback
func OnFrameRecvCallback(ptr unsafe.Pointer, streamID C.int) C.int {
	conn := (*Conn)(ptr)
	conn.onFrameRecv(int(streamID))
	return 0
}
