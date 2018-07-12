package nghttp2

/*
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
	"io"
	"sync"
	"time"
	"unsafe"
)

// dataProvider provider data for libnghttp2 library
// libnghttp2 callback will Read to read the data,
// application call Write to provider data,
// application call Close will cause Read return io.EOF
type dataProvider struct {
	buf      *bytes.Buffer
	closed   bool
	lock     *sync.Mutex
	sessLock *sync.Mutex
	session  *C.nghttp2_session
	streamID int
	deferred bool
}

// Read read from data provider
func (dp *dataProvider) Read(buf []byte) (n int, err error) {
	dp.lock.Lock()
	defer dp.lock.Unlock()

	n, err = dp.buf.Read(buf)
	if err != nil && !dp.closed {
		return 0, errAgain
	}
	return
}

// Write provider data for data provider
func (dp *dataProvider) Write(buf []byte) (n int, err error) {
	dp.lock.Lock()
	defer dp.lock.Unlock()

	if dp.closed {
		return 0, io.EOF
	}

	if dp.deferred {
		dp.sessLock.Lock()
		C.nghttp2_session_resume_data(dp.session, C.int(dp.streamID))
		dp.sessLock.Unlock()

		dp.deferred = false
	}
	return dp.buf.Write(buf)
}

// Close end to provide data
func (dp *dataProvider) Close() error {
	dp.lock.Lock()
	defer dp.lock.Unlock()

	if dp.closed {
		return nil
	}
	dp.closed = true
	//log.Printf("dp close stream %d", dp.streamID)
	if dp.deferred {
		dp.sessLock.Lock()
		C.nghttp2_session_resume_data(dp.session, C.int(dp.streamID))
		dp.sessLock.Unlock()

		dp.deferred = false
	}
	return nil
}

func newDataProvider(sessionLock *sync.Mutex) (
	*dataProvider, *C.nghttp2_data_provider) {
	dp := &dataProvider{
		buf:      new(bytes.Buffer),
		lock:     new(sync.Mutex),
		sessLock: sessionLock,
	}
	cdp := C.new_data_provider(C.size_t(uintptr(unsafe.Pointer(dp))))
	return dp, cdp
}

// bodyProvider provide data for http body
// Read will block when data not yet avaliable
type bodyProvider struct {
	buf    *bytes.Buffer
	closed bool
	lock   *sync.Mutex
}

// Read read data from provider
// will block when data not yet avaliable
func (bp *bodyProvider) Read(buf []byte) (int, error) {
	var delay = 100 * time.Millisecond

	for {
		bp.lock.Lock()
		n, err := bp.buf.Read(buf)
		bp.lock.Unlock()
		if err != nil && !bp.closed {
			time.Sleep(delay)
			continue
		}
		return n, err
	}
}

// Write provide data for dataProvider
// libnghttp2 data chunk recv callback will call this
func (bp *bodyProvider) Write(buf []byte) (int, error) {
	bp.lock.Lock()
	defer bp.lock.Unlock()

	return bp.buf.Write(buf)
}

// Close end to provide data
func (bp *bodyProvider) Close() error {
	bp.lock.Lock()
	defer bp.lock.Unlock()

	bp.closed = true
	return nil
}

// setNvArray set the array for nghttp2_nv array
func setNvArray(a *C.struct_nv_array, index int,
	name, value string, flags int) {
	cname := C.CString(name)
	cvalue := C.CString(value)
	cnamelen := C.size_t(len(name))
	cvaluelen := C.size_t(len(value))
	cflags := C.int(flags)

	// note: cname and cvalue will freed in C.delete_nv_array

	C.nv_array_set(a, C.int(index), cname,
		cvalue, cnamelen, cvaluelen, cflags)
}
