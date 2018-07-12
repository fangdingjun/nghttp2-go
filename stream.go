package nghttp2

/*
#include "_nghttp2.h"
*/
import "C"
import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"unsafe"
)

// ClientStream http2 client stream
type ClientStream struct {
	streamID int
	conn     *ClientConn
	cdp      *C.nghttp2_data_provider
	dp       *dataProvider
	// application read data from stream
	//r *io.PipeReader
	// recv stream data from session
	//w      *io.PipeWriter
	res    *http.Response
	resch  chan *http.Response
	errch  chan error
	closed bool
	lock   *sync.Mutex
}

// Read read stream data
func (s *ClientStream) Read(buf []byte) (n int, err error) {
	if s.closed || s.res == nil || s.res.Body == nil {
		return 0, io.EOF
	}
	return s.res.Body.Read(buf)
}

// Write write data to stream
func (s *ClientStream) Write(buf []byte) (n int, err error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.dp != nil {
		return s.dp.Write(buf)
	}
	return 0, fmt.Errorf("empty data provider")
}

// Close close the stream
func (s *ClientStream) Close() error {
	//s.lock.Lock()
	//defer s.lock.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := io.EOF
	//log.Printf("close stream %d", int(s.streamID))
	select {
	case s.errch <- err:
	default:
	}
	//log.Println("close stream resch")
	//close(s.resch)
	//log.Println("close stream errch")
	//close(s.errch)
	//log.Println("close pipe w")
	if s.res != nil && s.res.Body != nil {
		s.res.Body.Close()
	}
	//log.Println("close stream done")
	if s.dp != nil {
		s.dp.Close()
		//s.conn.lock.Lock()
		//C.nghttp2_submit_rst_stream(s.conn.session, 0, C.int(s.streamID), 0)
		//s.conn.lock.Unlock()
		s.dp = nil
	}
	if s.cdp != nil {
		C.free(unsafe.Pointer(s.cdp))
		s.cdp = nil
	}

	s.conn.lock.Lock()
	defer s.conn.lock.Unlock()
	if _, ok := s.conn.streams[s.streamID]; ok {
		delete(s.conn.streams, s.streamID)
	}
	return nil
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

// Write write data to stream,
// implements http.ResponseWriter
func (s *ServerStream) Write(buf []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}

	if !s.responseSend {
		s.WriteHeader(http.StatusOK)
	}
	return s.dp.Write(buf)
}

// WriteHeader set response code and send reponse,
// implements http.ResponseWriter
func (s *ServerStream) WriteHeader(code int) {
	if s.closed {
		return
	}

	if s.responseSend {
		return
	}

	s.responseSend = true
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

	dp, cdp = newDataProvider(s.conn.lock)
	dp.streamID = s.streamID
	dp.session = s.conn.session

	s.dp = dp
	s.cdp = cdp

	s.conn.lock.Lock()
	ret := C.nghttp2_submit_response(
		s.conn.session, C.int(s.streamID), nva.nv, C.size_t(nvIndex), cdp)
	s.conn.lock.Unlock()

	C.delete_nv_array(nva)
	if int(ret) < 0 {
		panic(fmt.Sprintf("sumit response error %s",
			C.GoString(C.nghttp2_strerror(ret))))
	}
	//log.Printf("stream %d send response", s.streamID)
}

// Header return the http.Header,
// implements http.ResponseWriter
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
	s.closed = true

	//C.nghttp2_submit_rst_stream(s.conn.session, 0, C.int(s.streamID), 0)
	if s.req.Body != nil {
		s.req.Body.Close()
	}

	if s.dp != nil {
		s.dp.Close()
		s.dp = nil
	}

	if s.cdp != nil {
		C.free(unsafe.Pointer(s.cdp))
		s.cdp = nil
	}

	s.conn.lock.Lock()
	s.conn.lock.Unlock()

	if _, ok := s.conn.streams[s.streamID]; ok {
		delete(s.conn.streams, s.streamID)
	}
	//log.Printf("stream %d closed", s.streamID)
	return nil
}
