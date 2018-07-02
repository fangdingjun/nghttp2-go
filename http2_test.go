package nghttp2

import (
	"bytes"
	"crypto/tls"
	"log"
	"net/http"
	"net/url"
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
	h2conn, err := NewClientConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	param := url.Values{}
	param.Add("e", "b")
	param.Add("f", "d")
	data := bytes.NewReader([]byte(param.Encode()))
	req, _ := http.NewRequest("POST",
		"https://www.simicloud.com/media/httpbin/post?a=b&c=d",
		data)
	log.Printf("%+v", req)
	req.Header.Set("accept", "*/*")
	req.Header.Set("user-agent", "go-nghttp2/1.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := h2conn.CreateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Write(os.Stderr)

	req, _ = http.NewRequest("GET",
		"https://www.simicloud.com/media/httpbin/get?a=b&c=d", nil)
	res, err = h2conn.CreateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Write(os.Stderr)

	log.Println("end")
}
