package anthropic

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Match the pinned SDK's 32MiB scanner ceiling and also bound aggregate blocks.
// This adapter repairs framing only: data bytes are never parsed or rewritten.
const responsesSSEFrameLimit = 32 << 20

var errResponsesSSEFrameTooLarge = errors.New("Responses SSE block exceeds size limit")

type responsesHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Wrap OUTSIDE the raw HTTP trace/idle guard. Comments are network activity even
// when they do not dispatch a semantic event. No global SDK decoder registry is
// changed; other protocols and non-streaming responses remain untouched.
type responsesSSEClient struct{ inner responsesHTTPDoer }

func (c responsesSSEClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.inner.Do(request)
	if err != nil || response == nil || response.Body == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, err
	}
	media, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || !strings.EqualFold(media, "text/event-stream") {
		return response, nil
	}
	response.Body = newResponsesSSEBody(response.Body, responsesSSEFrameLimit)
	response.ContentLength = -1
	response.Header.Del("Content-Length")
	return response, nil
}

type responsesSSEBody struct {
	source   io.ReadCloser
	reader   *bufio.Reader
	limit    int
	pending  []byte
	terminal error
}

func newResponsesSSEBody(source io.ReadCloser, limit int) *responsesSSEBody {
	return &responsesSSEBody{source: source, reader: bufio.NewReader(source), limit: limit}
}
func (s *responsesSSEBody) Close() error { return s.source.Close() }
func (s *responsesSSEBody) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if len(s.pending) == 0 && s.terminal == nil {
		s.pending, s.terminal = s.nextDataBlock()
	}
	if len(s.pending) > 0 {
		n := copy(dst, s.pending)
		s.pending = s.pending[n:]
		if len(s.pending) == 0 {
			s.pending = nil
		}
		return n, nil
	}
	return 0, s.terminal
}
func (s *responsesSSEBody) nextDataBlock() ([]byte, error) {
	var block []byte
	lineStart := 0
	hasData := false
	for {
		piece, err := s.reader.ReadSlice('\n')
		if len(piece) > s.limit-len(block) {
			return nil, errResponsesSSEFrameTooLarge
		}
		block = append(block, piece...)
		if err == bufio.ErrBufferFull {
			continue
		}
		line := bytes.TrimSuffix(block[lineStart:], []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		// Recognize, but do not strip, a leading BOM so it cannot cause a data
		// block to be silently dropped. The SDK still owns actual field decoding.
		field := line
		if lineStart == 0 {
			field = bytes.TrimPrefix(field, []byte{0xef, 0xbb, 0xbf})
		}
		hasData = hasData || bytes.Equal(field, []byte("data")) || bytes.HasPrefix(field, []byte("data:"))
		if err != nil {
			// Preserve partial data + the original transport error. Never manufacture
			// a delimiter or turn a truncated data block into a complete JSON event.
			if hasData {
				return block, err
			}
			return nil, err
		}
		if len(line) == 0 {
			if hasData {
				return block, nil
			}
			block = block[:0]
			lineStart = 0
			continue // A comment/blank/no-data block is not a JSON event.
		}
		lineStart = len(block)
	}
}
