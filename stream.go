package nghttp2

/*
#include "_nghttp2.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unsafe"
)

type stream struct {
	streamID   int
	conn       *Conn
	dp         *dataProvider
	bp         *bodyProvider
	request    *http.Request
	response   *http.Response
	resch      chan *http.Response
	headersEnd bool
	streamEnd  bool
	closed     bool
	cdp        C.nghttp2_data_provider
}

var _ net.Conn = &stream{}

func (s *stream) free() {
	//log.Printf("stream free %d", s.streamID)
	if !s.closed {
		s.Close()
	}
	s.conn = nil
	if s.dp != nil {
		s.dp.buf = nil
		s.dp.lock = nil
		s.dp.sessLock = nil
		s.dp.session = nil
		s.dp = nil
	}
	if s.bp != nil {
		s.bp.buf = nil
		s.bp.lock = nil
		s.bp = nil
	}
	s.request = nil
	s.response = nil
	s.resch = nil
}

func (s *stream) Read(buf []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.bp != nil {
		return s.bp.Read(buf)
	}
	return 0, errors.New("empty body")
}

func (s *stream) WriteHeader(code int) {
	if s.closed {
		return
	}
	if s.response == nil {
		s.response = &http.Response{
			Proto:      "http/2",
			ProtoMajor: 2,
			ProtoMinor: 0,
			Header:     make(http.Header),
		}
	}
	if s.response.StatusCode != 0 {
		return
	}

	s.response.StatusCode = code
	s.response.Status = http.StatusText(code)

	nv := []C.nghttp2_nv{}
	nv = append(nv, newNV(":status", fmt.Sprintf("%d", code)))
	for k, v := range s.response.Header {
		_k := strings.ToLower(k)
		if _k == "host" || _k == "connection" || _k == "transfer-encoding" {
			continue
		}
		nv = append(nv, newNV(k, v[0]))
	}

	s.cdp = C.nghttp2_data_provider{}
	s.dp = newDataProvider(unsafe.Pointer(&s.cdp), s.conn.lock, 0)
	s.dp.session = s.conn.session
	s.dp.streamID = s.streamID

	s.conn.lock.Lock()
	if s.conn.closed {
		s.conn.lock.Unlock()
		return
	}
	ret := C._nghttp2_submit_response(s.conn.session, C.int(s.streamID),
		C.size_t(uintptr(unsafe.Pointer(&nv[0]))), C.size_t(len(nv)), &s.cdp)
	s.conn.lock.Unlock()

	if int(ret) < 0 {
		panic(fmt.Sprintf("submit response error: %s", C.GoString(C.nghttp2_strerror(ret))))
	}
}

func (s *stream) Header() http.Header {
	if s.response == nil {
		s.response = &http.Response{
			Proto:      "http/2",
			ProtoMajor: 2,
			ProtoMinor: 0,
			Header:     make(http.Header),
		}
	}
	return s.response.Header
}

func (s *stream) Write(buf []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.conn.isServer && (s.response == nil || s.response.StatusCode == 0) {
		s.WriteHeader(http.StatusOK)
	}

	if s.dp != nil {
		return s.dp.Write(buf)
	}
	return 0, errors.New("empty dp")
}

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.dp != nil {
		s.dp.Close()
	}
	if s.bp != nil {
		s.bp.Close()
	}
	//s.conn.lock.Lock()
	//if _, ok := s.conn.streams[s.streamID]; ok {
	//	delete(s.conn.streams, s.streamID)
	///}
	//s.conn.lock.Unlock()
	if s.request != nil && s.request.Method == "CONNECT" {
		//log.Println("rst stream")
		s.conn.lock.Lock()
		C.nghttp2_submit_rst_stream(s.conn.session, 0, C.int(s.streamID), 8)
		s.conn.lock.Unlock()
	}
	return nil
}

func (s *stream) LocalAddr() net.Addr {
	return nil
}

func (s *stream) RemoteAddr() net.Addr {
	return nil
}

func (s *stream) SetDeadline(t time.Time) error {
	return errors.New("not implement")
}

func (s *stream) SetReadDeadline(t time.Time) error {
	return errors.New("not implement")
}

func (s *stream) SetWriteDeadline(t time.Time) error {
	return errors.New("not implement")
}
