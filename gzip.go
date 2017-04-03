package gziphandler

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/golang/gddo/httputil/header"
)

// defaultMinSize defines the minimum size to reach to enable compression.
const defaultMinSize = 512

// gzipWriterPools stores a sync.Pool for each compression level for reuse of
// gzip.Writers. Use poolIndex to covert a compression level to an index into
// gzipWriterPools.
var gzipWriterPools [gzip.BestCompression - gzip.BestSpeed + 2]*sync.Pool

func init() {
	for i := gzip.BestSpeed; i <= gzip.BestCompression; i++ {
		addLevelPool(i)
	}
	addLevelPool(gzip.DefaultCompression)
}

// poolIndex maps a compression level to its index into gzipWriterPools. It
// assumes that level is a valid gzip compression level.
func poolIndex(level int) int {
	// gzip.DefaultCompression == -1, so we need to treat it special.
	if level == gzip.DefaultCompression {
		return gzip.BestCompression - gzip.BestSpeed + 1
	}
	return level - gzip.BestSpeed
}

func addLevelPool(level int) {
	gzipWriterPools[poolIndex(level)] = &sync.Pool{
		New: func() interface{} {
			// NewWriterLevel only returns error on a bad level, we are guaranteeing
			// that this will be a valid level so it is okay to ignore the returned
			// error.
			w, _ := gzip.NewWriterLevel(nil, level)
			return w
		},
	}
}

// gzipResponseWriter provides an http.ResponseWriter interface, which gzips
// bytes before writing them to the underlying response. This doesn't close the
// writers, so don't forget to do that.
// It can be configured to skip response smaller than minSize.
type gzipResponseWriter struct {
	http.ResponseWriter
	index int // Index for gzipWriterPools.
	gw    *gzip.Writer

	code int // Saves the WriteHeader value.

	minSize      int    // Specifed the minimum response size to gzip. If the response length is bigger than this value, it is compressed.
	buf          []byte // Holds the first part of the write before reaching the minSize or the end of the write.
	bytesWritten int    // Keep trace of the numbers of bytes written.
}

// Write appends data to the gzip writer.
func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	h := w.Header()

	// If content type is not set.
	if _, ok := h["Content-Type"]; !ok {
		// It infer it from the uncompressed body.
		h.Set("Content-Type", http.DetectContentType(b))
	}

	// GZIP responseWriter is initialized. Use the GZIP responseWriter.
	if w.gw != nil {
		n, err := w.gw.Write(b)

		// Update the numbers of bytes written.
		w.bytesWritten += n

		return n, err
	}

	// Save the write into a buffer for later use in GZIP responseWriter (if content is long enough) or at close with regular responseWriter.
	w.buf = append(w.buf, b...)

	// If the global writes are bigger than the minSize, compression is enable.
	if w.bytesWritten+len(b) > w.minSize {
		return w.startGzip()
	}

	return len(b), nil
}

// startGzip initialize any GZIP specific informations.
func (w *gzipResponseWriter) startGzip() (int, error) {
	h := w.Header()

	// Set the GZIP header.
	h.Set("Content-Encoding", "gzip")

	// if the Content-Length is already set, then calls to Write on gzip
	// will fail to set the Content-Length header since its already set
	// See: https://github.com/golang/go/issues/14975.
	h.Del("Content-Length")

	// Write the header to gzip response.
	w.writeHeader()

	// Initialize the GZIP response.
	w.init()

	// Flush the buffer into the gzip reponse.
	n, err := w.gw.Write(w.buf)
	// Empty the buffer.
	w.buf = nil

	// Return the numbers of bytes writen and the error if any.
	return n, err
}

// WriteHeader just saves the response code until close or GZIP effective writes.
func (w *gzipResponseWriter) WriteHeader(code int) {
	w.code = code
}

// writeHeader uses the saved code to send it to the ResponseWriter.
func (w *gzipResponseWriter) writeHeader() {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.code)
}

// init graps a new gzip writer from the gzipWriterPool and writes the correct
// content encoding header.
func (w *gzipResponseWriter) init() {
	// Bytes written during ServeHTTP are redirected to this gzip writer
	// before being written to the underlying response.
	gzw := gzipWriterPools[w.index].Get().(*gzip.Writer)
	gzw.Reset(w.ResponseWriter)
	w.gw = gzw
}

// Close will close the gzip.Writer and will put it back in the gzipWriterPool.
func (w *gzipResponseWriter) Close() error {
	// Buffer not nil means the regular response must be returned.
	if w.buf != nil {
		w.writeHeader()
		// Make the write into the regular response.
		_, writeErr := w.ResponseWriter.Write(w.buf)
		// Returns the error if any at write.
		if writeErr != nil {
			return fmt.Errorf("gziphandler: write to regular responseWriter at close gets error: %q", writeErr.Error())
		}
	}

	// If the GZIP responseWriter is not set no needs to close it.
	if w.gw == nil {
		return nil
	}

	err := w.gw.Close()
	gzipWriterPools[w.index].Put(w.gw)
	w.gw = nil
	return err
}

// Flush flushes the underlying *gzip.Writer and then the underlying
// http.ResponseWriter if it is an http.Flusher. This makes GzipResponseWriter
// an http.Flusher.
func (w *gzipResponseWriter) Flush() {
	if w.gw != nil {
		w.gw.Flush()
	}

	if fw, ok := w.ResponseWriter.(http.Flusher); ok {
		fw.Flush()
	}
}

// Hijack implements http.Hijacker. If the underlying ResponseWriter is a
// Hijacker, its Hijack method is returned. Otherwise an error is returned.
func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("http.Hijacker interface is not supported")
}

// verify Hijacker interface implementation
var _ http.Hijacker = &gzipResponseWriter{}

// Push initiates an HTTP/2 server push.
// Push returns ErrNotSupported if the client has disabled push or if push
// is not supported on the underlying connection.
func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if ok && pusher != nil {
		return pusher.Push(target, setAcceptEncodingForPushOptions(opts))
	}
	return http.ErrNotSupported
}

// Gzip wraps an HTTP handler, to transparently gzip the response body if
// the client supports it (via the Accept-Encoding header). This will compress at
// the default compression level.
func Gzip(h http.Handler) http.Handler {
	h, err := GzipWithLevel(h, gzip.DefaultCompression)
	if err != nil {
		panic(err)
	}

	return h
}

// GzipWithLevel wraps an HTTP handler, to transparently gzip the response
// body if the client supports it (via the Accept-Encoding header). This will
// compress at the given gzip compression level.
func GzipWithLevel(h http.Handler, level int) (http.Handler, error) {
	return GzipWithLevelAndMinSize(h, level, defaultMinSize)
}

// GzipWithLevelAndMinSize wraps an HTTP handler, to transparently gzip the
// response body if the client supports it (via the Accept-Encoding header).
// This will compress at the given gzip compression level. The resource will
// not be compressed unless it is larger than minSize.
func GzipWithLevelAndMinSize(h http.Handler, level, minSize int) (http.Handler, error) {
	if level != gzip.DefaultCompression && (level < gzip.BestSpeed || level > gzip.BestCompression) {
		return nil, fmt.Errorf("invalid compression level requested: %d", level)
	}

	if minSize < 0 {
		return nil, fmt.Errorf("minimum size must be more than zero")
	}

	index := poolIndex(level)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")

		var acceptsGzip bool
		for _, spec := range header.ParseAccept(r.Header, "Accept-Encoding") {
			if spec.Value == "gzip" && spec.Q > 0 {
				acceptsGzip = true
				break
			}
		}

		if acceptsGzip {
			gw := &gzipResponseWriter{
				ResponseWriter: w,
				index:          index,
				minSize:        minSize,

				buf: []byte{},
			}
			defer gw.Close()

			h.ServeHTTP(gw, r)
		} else {
			h.ServeHTTP(w, r)
		}
	}), nil
}

// setAcceptEncodingForPushOptions sets "Accept-Encoding" : "gzip" for PushOptions without overriding existing headers.
func setAcceptEncodingForPushOptions(opts *http.PushOptions) *http.PushOptions {

	if opts == nil {
		opts = &http.PushOptions{
			Header: http.Header{
				"Accept-Encoding": []string{"gzip"},
			},
		}
		return opts
	}

	if opts.Header == nil {
		opts.Header = http.Header{
			"Accept-Encoding": []string{"gzip"},
		}
		return opts
	}

	if encoding := opts.Header.Get("Accept-Encoding"); encoding == "" {
		opts.Header.Add("Accept-Encoding", "gzip")
		return opts
	}

	return opts
}
