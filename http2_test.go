package nghttp2

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
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

func TestHttp2Server(t *testing.T) {
	cert, err := tls.LoadX509KeyPair("testdata/server.crt", "testdata/server.key")
	if err != nil {
		t.Fatal(err)
	}

	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	addr := l.Addr().String()
	go func() {
		http.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
			log.Printf("%+v", r)
			hdr := w.Header()
			hdr.Set("content-type", "text/plain")
			hdr.Set("aa", "bb")
			d, err := ioutil.ReadAll(r.Body)
			if err != nil {
				log.Println(err)
				return
			}
			w.Write(d)
		})
		for {
			c, err := l.Accept()
			if err != nil {
				break
			}
			h2conn, err := NewServerConn(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			log.Printf("%+v", h2conn)
			go h2conn.Run()
		}
	}()
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		NextProtos:         []string{"h2"},
		ServerName:         "localhost",
		InsecureSkipVerify: true,
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
	d := bytes.NewBuffer([]byte("hello"))
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("https://%s/get?a=b&c=d", addr), d)
	req.Header.Add("User-Agent", "nghttp2/1.32")
	req.Header.Add("Content-Type", "text/palin")
	res, err := h2conn.CreateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expect http code %d, got %d", http.StatusOK, res.StatusCode)
	}
	defer res.Body.Close()
	log.Printf("%+v", res)
	data, err := ioutil.ReadAll(res.Body)
	log.Println(string(data))
	if err != nil {
		t.Error(err)
	}
	if string(data) != "hello" {
		t.Errorf("expect %s, got %s", "hello", string(data))
	}
}
