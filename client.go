package nghttp2

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Transport the nghttp2 RoundTripper implement
type Transport struct {
	TLSConfig *tls.Config
	DialTLS   func(network, addr string, cfg *tls.Config) (*tls.Conn, error)
	cacheConn map[string]*Conn
	mu        sync.Mutex
}

// RoundTrip send req and get res
func (tr *Transport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	h2conn, err := tr.getConn(req)
	if err != nil {
		return nil, err
	}
	return h2conn.RoundTrip(req)
}

func (tr *Transport) getConn(req *http.Request) (*Conn, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.cacheConn == nil {
		tr.cacheConn = map[string]*Conn{}
	}
	k := req.URL.Host
	if c, ok := tr.cacheConn[k]; ok {
		if c.CanTakeNewRequest() {
			return c, nil
		}
		delete(tr.cacheConn, k)
		c.Close()
	}
	c, err := tr.createConn(k)
	if err == nil {
		tr.cacheConn[k] = c
	}
	return c, err
}

func (tr *Transport) createConn(host string) (*Conn, error) {
	dial := tls.Dial
	if tr.DialTLS != nil {
		dial = tr.DialTLS
	}
	cfg := tr.TLSConfig
	if cfg == nil {
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			h = host
		}
		cfg = &tls.Config{
			ServerName: h,
			NextProtos: []string{"h2"},
		}
	}
	if !strings.Contains(host, ":") {
		host = fmt.Sprintf("%s:443", host)
	}
	conn, err := dial("tcp", host, cfg)
	if err != nil {
		return nil, err
	}
	if err = conn.Handshake(); err != nil {
		return nil, err
	}
	state := conn.ConnectionState()
	if state.NegotiatedProtocol != "h2" {
		conn.Close()
		return nil, errors.New("http2 is not supported")
	}

	return Client(conn)
}
