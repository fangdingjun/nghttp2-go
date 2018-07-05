package nghttp2

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"

	"golang.org/x/net/http2"
)

func TestHttp2Client(t *testing.T) {
	conn, err := tls.Dial("tcp", "nghttp2.org:443", &tls.Config{
		NextProtos: []string{"h2"},
		ServerName: "nghttp2.org",
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
		"https://nghttp2.org/httpbin/post?a=b&c=d",
		data)
	log.Printf("%+v", req)
	req.Header.Set("accept", "*/*")
	req.Header.Set("user-agent", "go-nghttp2/1.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := h2conn.CreateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expect %d, got %d", http.StatusOK, res.StatusCode)
	}
	log.Printf("%+v", res)
	//res.Write(os.Stderr)

	req, _ = http.NewRequest("GET",
		"https://nghttp2.org/httpbin/get?a=b&c=d", nil)
	res, err = h2conn.CreateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expect %d, got %d", http.StatusOK, res.StatusCode)
	}
	log.Printf("%+v", res)
	//res.Write(os.Stderr)

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
		http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
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
	client := &http.Client{
		Transport: &http2.Transport{
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				conn, err := tls.Dial(network, addr, &tls.Config{
					NextProtos:         []string{"h2", "http/1.1"},
					InsecureSkipVerify: true,
				})
				if err := conn.Handshake(); err != nil {
					return nil, err
				}
				return conn, err
			},
		},
	}
	d := bytes.NewBuffer([]byte("hello"))
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("https://%s/test?a=b&c=d", addr), d)
	req.Header.Add("User-Agent", "nghttp2/1.32")
	req.Header.Add("Content-Type", "text/palin")
	res, err := client.Do(req)
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

func TestHttp2Handler(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		TLSConfig: &tls.Config{
			NextProtos: []string{"h2", "http/1.1"},
		},
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){
			"h2": HTTP2Handler,
		},
	}
	defer srv.Close()

	testdata := "asc fasdf32ddfasfff\r\nassdf312313"
	addr := l.Addr().String()
	go func() {
		http.HandleFunc("/test2", func(w http.ResponseWriter, r *http.Request) {
			hdr := w.Header()
			hdr.Set("content-type", "text/plain")
			hdr.Set("aa", "bb")
			fmt.Fprintf(w, testdata)
		})
		http.Handle("/", http.FileServer(http.Dir("/")))
		srv.ServeTLS(l, "testdata/server.crt", "testdata/server.key")
	}()
	client := &http.Client{
		Transport: &http2.Transport{
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				conn, err := tls.Dial(network, addr, &tls.Config{
					NextProtos:         []string{"h2", "http/1.1"},
					InsecureSkipVerify: true,
				})
				if err := conn.Handshake(); err != nil {
					return nil, err
				}
				return conn, err
			},
		},
	}
	u := fmt.Sprintf("https://%s/test2", addr)
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("http error %d", resp.StatusCode)
	}
	if resp.TLS == nil {
		t.Errorf("not tls")
	}
	if resp.TLS.NegotiatedProtocol != "h2" {
		t.Errorf("http2 is not enabled")
	}
	d, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
	}
	if string(d) != testdata {
		t.Errorf("expect %s, got %s", testdata, string(d))
	}
	if resp.Header.Get("aa") != "bb" {
		t.Errorf("expect header not found")
	}
	//io.Copy(os.Stdout, resp.Body)
	resp.Write(os.Stdout)
}
