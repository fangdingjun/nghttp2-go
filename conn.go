package nghttp2

/*
#cgo pkg-config: libnghttp2
#include "_nghttp2.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"net"
	"net/http"
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
	handler     http.Handler
	lock        *sync.Mutex
	err         error
	errch       chan error
	exitch      chan struct{}
}

// RoundTrip submit http request and return the response
func (c *Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errors.New("not implement")
}

// Connect submit connect request
func (c *Conn) Connect(addr string) (net.Conn, error) {
	return nil, errors.New("not implement")
}

// Run run the event loop
func (c *Conn) Run() {
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

// Close close the connection
func (c *Conn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	for _, s := range c.streams {
		s.Close()
	}
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
	type data struct {
		buf []byte
		err error
	}

	var ret C.ssize_t
	var err error
	var d data

	datach := make(chan data)

	go func() {
		d1 := data{}
		var n int
		var err1 error
		for {
			buf := make([]byte, 16*1024)
			n, err1 = c.conn.Read(buf)
			d1.buf = buf[:n]
			d1.err = err1
			datach <- d1
		}
	}()

	for {
		select {
		case <-c.exitch:
			return
		case d = <-datach:
			if d.err != nil {
				c.errorNotify(d.err)
				return
			}
			c.lock.Lock()
			ret = C.nghttp2_session_mem_recv(c.session,
				(*C.uchar)(unsafe.Pointer(&d.buf[0])), C.size_t(len(d.buf)))
			c.lock.Unlock()
			if int(ret) < 0 {
				err = fmt.Errorf("http2 recv error: %s", C.GoString(C.nghttp2_strerror(C.int(ret))))
				c.errorNotify(err)
				return
			}
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
			time.Sleep(delay)
		}
	}
}
