package gziphandler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	smallTestBody = "aaabbcaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc"
	testBody      = "aaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbcccaaabbbccc"
)

func TestGzipHandler(t *testing.T) {
	// This just exists to provide something for GzipHandler to wrap.
	handler := newTestHandler(testBody)

	// requests without accept-encoding are passed along as-is

	req1, _ := http.NewRequest("GET", "/whatever", nil)
	resp1 := httptest.NewRecorder()
	handler.ServeHTTP(resp1, req1)
	res1 := resp1.Result()

	assert.Equal(t, 200, res1.StatusCode)
	assert.Equal(t, "", res1.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res1.Header.Get("Vary"))
	assert.Equal(t, testBody, resp1.Body.String())

	// but requests with accept-encoding:gzip are compressed if possible

	req2, _ := http.NewRequest("GET", "/whatever", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	resp2 := httptest.NewRecorder()
	handler.ServeHTTP(resp2, req2)
	res2 := resp2.Result()

	assert.Equal(t, 200, res2.StatusCode)
	assert.Equal(t, "gzip", res2.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res2.Header.Get("Vary"))
	assert.Equal(t, gzipStrLevel(testBody, gzip.DefaultCompression), resp2.Body.Bytes())

	// content-type header is correctly set based on uncompressed body

	req3, _ := http.NewRequest("GET", "/whatever", nil)
	req3.Header.Set("Accept-Encoding", "gzip")
	res3 := httptest.NewRecorder()
	handler.ServeHTTP(res3, req3)

	assert.Equal(t, http.DetectContentType([]byte(testBody)), res3.Header().Get("Content-Type"))
}

func TestGzipHandlerAcceptEncodingCaseInsensitive(t *testing.T) {
	// This just exists to provide something for GzipHandler to wrap.
	handler := newTestHandler(testBody)

	req, _ := http.NewRequest("GET", "/whatever", nil)
	req.Header.Set("Accept-Encoding", "GZIP")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	res := resp.Result()

	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", res.Header.Get("Vary"))
	assert.Equal(t, gzipStrLevel(testBody, gzip.DefaultCompression), resp.Body.Bytes())
}

func TestNewGzipLevelHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, testBody)
	})

	for lvl := gzip.BestSpeed; lvl <= gzip.BestCompression; lvl++ {
		req, _ := http.NewRequest("GET", "/whatever", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp := httptest.NewRecorder()
		GzipWithLevel(handler, lvl).ServeHTTP(resp, req)
		res := resp.Result()

		assert.Equal(t, 200, res.StatusCode)
		assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
		assert.Equal(t, "Accept-Encoding", res.Header.Get("Vary"))
		assert.Equal(t, gzipStrLevel(testBody, lvl), resp.Body.Bytes())
	}
}

func TestGzipHandlerWithLevelReturnsErrorForInvalidLevels(t *testing.T) {
	assert.Panics(t, func() {
		GzipWithLevel(nil, -42)
	}, "GzipWithLevel did not panic on invalid level")

	assert.Panics(t, func() {
		GzipWithLevel(nil, 42)
	}, "GzipWithLevel did not panic on invalid level")
}

func TestGzipHandlerNoBody(t *testing.T) {
	tests := []struct {
		statusCode      int
		contentEncoding string
		bodyLen         int
	}{
		// Body must be empty.
		{http.StatusNoContent, "", 0},
		{http.StatusNotModified, "", 0},
		// Body is going to get gzip'd no matter what.
		{http.StatusOK, "", 0},
	}

	for num, test := range tests {
		handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(test.statusCode)
		}))

		rec := httptest.NewRecorder()
		// TODO: in Go1.7 httptest.NewRequest was introduced this should be used
		// once 1.6 is not longer supported.
		req := &http.Request{
			Method:     "GET",
			URL:        &url.URL{Path: "/"},
			Proto:      "HTTP/1.1",
			ProtoMinor: 1,
			RemoteAddr: "192.0.2.1:1234",
			Header:     make(http.Header),
		}
		req.Header.Set("Accept-Encoding", "gzip")
		handler.ServeHTTP(rec, req)

		body, err := ioutil.ReadAll(rec.Body)
		if err != nil {
			t.Fatalf("Unexpected error reading response body: %v", err)
		}

		header := rec.Header()
		assert.Equal(t, test.contentEncoding, header.Get("Content-Encoding"), fmt.Sprintf("for test iteration %d", num))
		assert.Equal(t, "Accept-Encoding", header.Get("Vary"), fmt.Sprintf("for test iteration %d", num))
		assert.Equal(t, test.bodyLen, len(body), fmt.Sprintf("for test iteration %d", num))
	}
}

func TestGzipHandlerContentLength(t *testing.T) {
	b := []byte(testBody)
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Write(b)
	}))
	// httptest.NewRecorder doesn't give you access to the Content-Length
	// header so instead, we create a server on a random port and make
	// a request to that instead
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		t.Fatalf("failed creating listen socket: %v", err)
	}
	defer ln.Close()
	srv := &http.Server{
		Handler: handler,
	}
	go srv.Serve(ln)

	req := &http.Request{
		Method: "GET",
		URL:    &url.URL{Path: "/", Scheme: "http", Host: ln.Addr().String()},
		Header: make(http.Header),
		Close:  true,
	}
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Unexpected error making http request: %v", err)
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading response body: %v", err)
	}

	l, err := strconv.Atoi(res.Header.Get("Content-Length"))
	if err != nil {
		t.Fatalf("Unexpected error parsing Content-Length: %v", err)
	}
	assert.Len(t, body, l)
	assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	assert.NotEqual(t, b, body)
}

func TestGzipHandlerMinSize(t *testing.T) {
	handler := GzipWithLevelAndMinSize(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			resp, _ := ioutil.ReadAll(r.Body)
			w.Write(resp)
			// Call write multiple times to pass through "chosenWriter"
			w.Write(resp)
			w.Write(resp)
		},
	), gzip.DefaultCompression, 13)

	// Run a test with size smaller than the limit
	b := bytes.NewBufferString("test")

	req1, _ := http.NewRequest("GET", "/whatever", b)
	req1.Header.Add("Accept-Encoding", "gzip")
	resp1 := httptest.NewRecorder()
	handler.ServeHTTP(resp1, req1)
	res1 := resp1.Result()

	if res1.Header.Get("Content-Encoding") == "gzip" {
		t.Errorf("The response is compress and should not")
		return
	}

	// Run a test with size bigger than the limit
	b = bytes.NewBufferString(smallTestBody)

	req2, _ := http.NewRequest("GET", "/whatever", b)
	req2.Header.Add("Accept-Encoding", "gzip")
	resp2 := httptest.NewRecorder()
	handler.ServeHTTP(resp2, req2)
	res2 := resp2.Result()

	if res2.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("The response is not compress and should")
		return
	}

	assert.Panics(t, func() {
		GzipWithLevelAndMinSize(nil, gzip.DefaultCompression, -10)
	}, "GzipWithLevelAndMinSize did not panic on negative minSize")
}

func TestGzipDoubleClose(t *testing.T) {
	h := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// call close here and it'll get called again interally by
		// NewGzipLevelHandler's handler defer
		w.Write([]byte("test"))
		w.(io.Closer).Close()
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// the second close shouldn't have added the same writer
	// so we pull out 2 writers from the pool and make sure they're different
	w1 := h.(*handler).pool.Get()
	w2 := h.(*handler).pool.Get()
	// assert.NotEqual looks at the value and not the address, so we use regular ==
	assert.False(t, w1 == w2)
}

func TestStatusCodes(t *testing.T) {
	handler := Gzip(http.NotFoundHandler())
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	result := w.Result()
	if result.StatusCode != 404 {
		t.Errorf("StatusCode should have been 404 but was %d", result.StatusCode)
	}
}

func TestInferContentType(t *testing.T) {
	handler := GzipWithLevelAndMinSize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!doc")
		io.WriteString(w, "type html>")
	}), gzip.DefaultCompression, len("<!doctype html"))

	req1, _ := http.NewRequest("GET", "/whatever", nil)
	req1.Header.Add("Accept-Encoding", "gzip")
	resp1 := httptest.NewRecorder()
	handler.ServeHTTP(resp1, req1)
	res1 := resp1.Result()

	const expect = "text/html; charset=utf-8"
	if ct := res1.Header.Get("Content-Type"); ct != expect {
		t.Error("Infering Content-Type failed for buffered response")
		t.Logf("Expected: %s", expect)
		t.Logf("Got:      %s", ct)
	}
}

func TestInferContentTypeUncompressed(t *testing.T) {
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!doctype html>")
	}))

	req1, _ := http.NewRequest("GET", "/whatever", nil)
	req1.Header.Add("Accept-Encoding", "gzip")
	resp1 := httptest.NewRecorder()
	handler.ServeHTTP(resp1, req1)
	res1 := resp1.Result()

	const expect = "text/html; charset=utf-8"
	if ct := res1.Header.Get("Content-Type"); ct != expect {
		t.Error("Infering Content-Type failed for uncompressed response")
		t.Logf("Expected: %s", expect)
		t.Logf("Got:      %s", ct)
	}
}

func TestPassThroughBigWrite(t *testing.T) {
	handler := GzipWithOptions(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			resp, _ := ioutil.ReadAll(r.Body)
			w.Write(resp)
		},
	), &Options{
		Level:       DefaultCompression,
		MinSize:     10,
		CanCompress: func(http.Header) bool { return false },
	})

	// Run a test with size bigger than the limit
	b := bytes.NewBufferString("01234567890123")

	req, _ := http.NewRequest("GET", "/whatever", b)
	req.Header.Add("Accept-Encoding", "gzip")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	res1 := resp.Result()

	if res1.Header.Get("Content-Encoding") == "gzip" {
		t.Errorf("The response is compress and should not")
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading response body: %v", err)
	}

	assert.Equal(t, string(body), "01234567890123")
}

func TestPassThroughSmallWrites(t *testing.T) {
	handler := GzipWithOptions(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			resp, _ := ioutil.ReadAll(r.Body)
			w.Write(resp)
			w.Write(resp)
			w.Write(resp)
		},
	), &Options{
		Level:       DefaultCompression,
		MinSize:     10,
		CanCompress: func(http.Header) bool { return false },
	})

	// Run a test with size smaller than the limit
	b := bytes.NewBufferString("012345678")

	req, _ := http.NewRequest("GET", "/whatever", b)
	req.Header.Add("Accept-Encoding", "gzip")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	res1 := resp.Result()

	if res1.Header.Get("Content-Encoding") == "gzip" {
		t.Errorf("The response is compress and should not")
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Unexpected error reading response body: %v", err)
	}

	assert.Equal(t, string(body), "012345678012345678012345678")
}

// --------------------------------------------------------------------

func BenchmarkGzipHandler_S2k(b *testing.B)   { benchmark(b, false, 2048) }
func BenchmarkGzipHandler_S20k(b *testing.B)  { benchmark(b, false, 20480) }
func BenchmarkGzipHandler_S100k(b *testing.B) { benchmark(b, false, 102400) }
func BenchmarkGzipHandler_P2k(b *testing.B)   { benchmark(b, true, 2048) }
func BenchmarkGzipHandler_P20k(b *testing.B)  { benchmark(b, true, 20480) }
func BenchmarkGzipHandler_P100k(b *testing.B) { benchmark(b, true, 102400) }

// --------------------------------------------------------------------

func gzipStrLevel(s string, lvl int) []byte {
	var b bytes.Buffer
	w, _ := gzip.NewWriterLevel(&b, lvl)
	io.WriteString(w, s)
	w.Close()
	return b.Bytes()
}

func benchmark(b *testing.B, parallel bool, size int) {
	bin, err := ioutil.ReadFile("testdata/benchmark.json")
	if err != nil {
		b.Fatal(err)
	}

	req, _ := http.NewRequest("GET", "/whatever", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler := newTestHandler(string(bin[:size]))

	if parallel {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				runBenchmark(b, req, handler)
			}
		})
	} else {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			runBenchmark(b, req, handler)
		}
	}
}

func runBenchmark(b *testing.B, req *http.Request, handler http.Handler) {
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if code := res.Code; code != 200 {
		b.Fatalf("Expected 200 but got %d", code)
	} else if blen := res.Body.Len(); blen < 500 {
		b.Fatalf("Expected complete response body, but got %d bytes", blen)
	}
}

func newTestHandler(body string) http.Handler {
	return Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
}
