package http3

import (
	"bufio"
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"github.com/lucas-clemente/quic-go"
	"github.com/lucas-clemente/quic-go/internal/utils"
	"github.com/marten-seemann/qpack"
)

// DataStreamer lets the caller take over the stream. After a call to DataStream
// the HTTP server library will not do anything else with the connection.
//
// It becomes the caller's responsibility to manage and close the stream.
//
// After a call to DataStream, the original Request.Body must not be used.
type DataStreamer interface {
	DataStream() quic.Stream
}

type ResponseWriter struct {
	stream         quic.Stream // needed for DataStream()
	bufferedStream *bufio.Writer

	header         http.Header
	status         int // status code passed to WriteHeader
	headerWritten  bool
	dataStreamUsed bool // set when DataSteam() is called

	logger utils.Logger
}

var (
	_ http.ResponseWriter = &ResponseWriter{}
	_ http.Flusher        = &ResponseWriter{}
	_ DataStreamer        = &ResponseWriter{}
)

func newResponseWriter(stream quic.Stream, logger utils.Logger) *ResponseWriter {
	return &ResponseWriter{
		header:         http.Header{},
		stream:         stream,
		bufferedStream: bufio.NewWriter(stream),
		logger:         logger,
	}
}

func (w *ResponseWriter) Header() http.Header {
	return w.header
}

func (w *ResponseWriter) WriteHeader(status int) {
	if w.headerWritten {
		return
	}

	if status < 100 || status >= 200 {
		w.headerWritten = true
	}
	w.status = status

	var headers bytes.Buffer
	enc := qpack.NewEncoder(&headers)
	enc.WriteField(qpack.HeaderField{Name: ":status", Value: strconv.Itoa(status)})

	for k, v := range w.header {
		for index := range v {
			enc.WriteField(qpack.HeaderField{Name: strings.ToLower(k), Value: v[index]})
		}
	}

	buf := &bytes.Buffer{}
	(&headersFrame{Length: uint64(headers.Len())}).Write(buf)
	w.logger.Infof("Responding with %d", status)
	if _, err := w.bufferedStream.Write(buf.Bytes()); err != nil {
		w.logger.Errorf("could not write headers frame: %s", err.Error())
	}
	if _, err := w.bufferedStream.Write(headers.Bytes()); err != nil {
		w.logger.Errorf("could not write header frame payload: %s", err.Error())
	}
	if !w.headerWritten {
		w.Flush()
	}
}

func (w *ResponseWriter) Write(p []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(200)
	}
	if !bodyAllowedForStatus(w.status) {
		return 0, http.ErrBodyNotAllowed
	}
	df := &dataFrame{Length: uint64(len(p))}
	buf := &bytes.Buffer{}
	df.Write(buf)
	if _, err := w.bufferedStream.Write(buf.Bytes()); err != nil {
		return 0, err
	}
	return w.bufferedStream.Write(p)
}

func (w *ResponseWriter) Flush() {
	if err := w.bufferedStream.Flush(); err != nil {
		w.logger.Errorf("could not flush to stream: %s", err.Error())
	}
}

func (w *ResponseWriter) usedDataStream() bool {
	return w.dataStreamUsed
}

func (w *ResponseWriter) DataStream() quic.Stream {
	w.dataStreamUsed = true
	w.Flush()
	return w.stream
}

// copied from http2/http2.go
// bodyAllowedForStatus reports whether a given response status code
// permits a body. See RFC 2616, section 4.4.
func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == 204:
		return false
	case status == 304:
		return false
	}
	return true
}
