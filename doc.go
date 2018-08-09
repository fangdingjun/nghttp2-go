/*Package nghttp2 is libnghttp2 binding for golang.

server example

	cert, err := tls.LoadX509KeyPair("testdata/server.crt", "testdata/server.key")
	if err != nil {
		log.Fatal(err)
	}

	l, err := tls.Listen("tcp", "127.0.0.1:1100", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	addr := l.Addr().String()

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
		h2conn, err := Server(c, nil)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%+v", h2conn)
		go h2conn.Run()
	}

client example

    conn, err := tls.Dial("tcp", "nghttp2.org:443", &tls.Config{
        NextProtos: []string{"h2"},
        ServerName: "nghttp2.org",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    if err := conn.Handshake(); err != nil{
        log.Fatal(err)
    }
    cstate := conn.ConnectionState()
    if cstate.NegotiatedProtocol != "h2" {
        log.Fatal("no http2 on server")
    }

    h2conn, err := Client(conn)
    if err != nil {
        log.Fatal(err)
    }

    param := url.Values{}
    param.Add("e", "b")
    param.Add("f", "d")
    data := bytes.NewReader([]byte(param.Encode()))
    req, _ := http.NewRequest("POST",
        "https://nghttp2.org/httpbin/post?a=b&c=d",
        data)

    log.Printf("%+v", req)

    req.Header.Set("user-agent", "go-nghttp2/1.0")
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    res, err := h2conn.RoundTrip(req)
    if err != nil {
        log.Fatal(err)
    }

    if res.StatusCode != http.StatusOK {
        log.Printf("expect %d, got %d", http.StatusOK, res.StatusCode)
    }
    res.Write(os.Stderr)


co-work with net/http example

    l, err := net.Listen("tcp", "127.0.0.1:1222")
    if err != nil {
        log.Fatal(err)
    }
    srv := &http.Server{
        TLSConfig: &tls.Config{
            NextProtos: []string{"h2", "http/1.1"},
        },
        TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){
            "h2": nghttp2.HTTP2Handler,
        },
    }
    defer srv.Close()

    srv.ServeTLS(l, "testdata/server.crt", "testdata/server.key")

see http2_test.go for more details
*/
package nghttp2
