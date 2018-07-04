nghttp2
======

nghttp2 is libnghttp2 binding for golang

see doc [here](https://godoc.org/github.com/fangdingjun/nghttp2)

server usage example:


	cert, err := tls.LoadX509KeyPair("testdata/server.crt", "testdata/server.key")
	if err != nil {
		t.Fatal(err)
	}

	l, err := tls.Listen("tcp", "127.0.0.1:1100", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	addr := l.Addr().String()

	http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		f("%+v", r)
		hdr := w.Header()
		hdr.Set("content-type", "text/plain")
		hdr.Set("aa", "bb")
		d, err := ioutil.ReadAll(r.Body)
		if err != nil {
			ln(err)
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
		f("%+v", h2conn)
		go h2conn.Run()
	}


client usage example:

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

    f("%+v", req)

    req.Header.Set("user-agent", "go-nghttp2/1.0")
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

    res, err := h2conn.CreateRequest(req)
    if err != nil {
        t.Fatal(err)
    }

    if res.StatusCode != http.StatusOK {
        t.Errorf("expect %d, got %d", http.StatusOK, res.StatusCode)
    }
    res.Write(os.Stderr)


co-work with net/http server example:

    l, err := net.Listen("tcp", "127.0.0.1:1222")
    if err != nil {
        t.Fatal(err)
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