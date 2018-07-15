package nghttp2

import (
	"errors"
	"net"
	"net/http"
	"time"
)

type stream struct {
	streamID int
	conn     *Conn
	dp       *dataProvider
	bp       *bodyProvider
	request  *http.Request
	response *http.Response
	resch    chan *http.Response
}

var _ net.Conn = &stream{}

func (s *stream) Read(buf []byte) (int, error) {
	return 0, errors.New("not implement")
}
func (s *stream) Write(buf []byte) (int, error) {
	if s.conn.isServer {
		return 0, errors.New("not implement")
	}
	return 0, errors.New("not implement")
}
func (s *stream) Close() error {
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
