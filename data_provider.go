package nghttp2

/*
#include "_nghttp2.h"
*/
import "C"
import (
	"bytes"
	"errors"
	"io"
	"log"
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
	if dp.buf == nil || dp.lock == nil || dp.sessLock == nil || dp.session == nil {
		log.Println("dp read invalid state")
		return 0, errors.New("invalid state")
	}
	dp.lock.Lock()
	defer dp.lock.Unlock()

	n, err = dp.buf.Read(buf)
	if err != nil && !dp.closed {
		//log.Println("deferred")
		dp.deferred = true
		return 0, errAgain
	}
	return
}

// Write provider data for data provider
func (dp *dataProvider) Write(buf []byte) (n int, err error) {
	if dp.buf == nil || dp.lock == nil || dp.sessLock == nil || dp.session == nil {
		log.Println("dp write invalid state")
		return 0, errors.New("invalid state")
	}

	// make sure the buffer not too large
	delay := 10 * time.Millisecond
	maxBufSize := 1 * 1024 * 1024
	for {
		dp.lock.Lock()
		_len := dp.buf.Len()
		closed := dp.closed
		dp.lock.Unlock()
		if closed {
			return 0, io.EOF
		}
		if _len < maxBufSize {
			break
		}
		time.Sleep(delay)
	}

	dp.lock.Lock()
	defer dp.lock.Unlock()

	//if dp.closed {
	//	return 0, io.EOF
	//}

	n, err = dp.buf.Write(buf)
	if dp.deferred {
		dp.sessLock.Lock()
		C.nghttp2_session_resume_data(dp.session, C.int(dp.streamID))
		dp.sessLock.Unlock()

		//log.Println("resume")
		dp.deferred = false
	}
	return
}

// Close end to provide data
func (dp *dataProvider) Close() error {
	if dp.buf == nil || dp.lock == nil || dp.sessLock == nil || dp.session == nil {
		log.Println("dp close, invalid state")
		return errors.New("invalid state")
	}
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

func newDataProvider(cdp unsafe.Pointer, sessionLock *sync.Mutex, t int) *dataProvider {
	dp := &dataProvider{
		buf:      new(bytes.Buffer),
		lock:     new(sync.Mutex),
		sessLock: sessionLock,
	}
	C.data_provider_set_callback(C.size_t(uintptr(cdp)),
		C.size_t(uintptr(unsafe.Pointer(dp))), C.int(t))
	return dp
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

func newNV(name, value string) C.nghttp2_nv {
	nv := C.nghttp2_nv{}
	nameArr := make([]byte, len(name)+1)
	valueArr := make([]byte, len(value)+1)
	copy(nameArr, []byte(name))
	copy(valueArr, []byte(value))

	nv.name = (*C.uchar)(unsafe.Pointer(&nameArr[0]))
	nv.value = (*C.uchar)(unsafe.Pointer(&valueArr[0]))
	nv.namelen = C.size_t(len(name))
	nv.valuelen = C.size_t(len(value))
	nv.flags = 0
	return nv
}
