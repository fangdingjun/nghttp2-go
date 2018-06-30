package nghttp2

import (
	"crypto/tls"
	"net/http"
	"os"
	"testing"
)

func TestHttp2Client(t *testing.T) {
	conn, err := tls.Dial("tcp", "www.simicloud.com:443", &tls.Config{
		NextProtos: []string{"h2"},
		ServerName: "www.simicloud.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cstate := conn.ConnectionState()
	if cstate.NegotiatedProtocol != "h2" {
		t.Fatal("no http2 on server")
	}
	h2conn, err := NewConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://www.simicloud.com/media/httpbin/get", nil)
	res, err := h2conn.NewRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Write(os.Stderr)
}
