package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

const maxSSEFrameBytes = 64 << 20

var rewriteFailureSequence atomic.Uint64

func rewriteSSEFrame(frame []byte, aliases map[string]string, maxOutputBytes int) ([]byte, bool, error) {
	if len(frame) == 0 || len(aliases) == 0 {
		return frame, false, nil
	}
	lines := bytes.SplitAfter(frame, []byte("\n"))
	dataIndexes := make(map[int]bool)
	data := make([][]byte, 0, 1)
	firstPrefix := []byte(nil)
	firstEnding := []byte(nil)
	eventType := ""
	for index, rawLine := range lines {
		content, ending := splitSSELine(rawLine)
		if bytes.HasPrefix(content, []byte("event:")) {
			value := content[len("event:"):]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			eventType = string(value)
		}
		if !bytes.HasPrefix(content, []byte("data:")) {
			continue
		}
		dataIndexes[index] = true
		value := content[len("data:"):]
		prefix := content[:len("data:")]
		if len(value) > 0 && value[0] == ' ' {
			prefix = content[:len("data:")+1]
			value = value[1:]
		}
		if len(data) == 0 {
			firstPrefix = prefix
			firstEnding = ending
		}
		data = append(data, value)
	}
	if len(data) == 0 {
		return frame, false, nil
	}
	joinedData := bytes.Join(data, []byte("\n"))
	if bytes.Equal(bytes.TrimSpace(joinedData), []byte("[DONE]")) {
		return frame, false, nil
	}
	rewritten, changed, err := restoreToolNamesInSSEJSON(joinedData, aliases, maxOutputBytes, eventType)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return frame, false, nil
	}

	removedDataBytes := int64(0)
	for index := range dataIndexes {
		removedDataBytes += int64(len(lines[index]))
	}
	replacementBytes := int64(len(firstPrefix) + len(rewritten) + len(firstEnding))
	finalFrameBytes := int64(len(frame)) - removedDataBytes + replacementBytes
	if maxOutputBytes < 0 || finalFrameBytes > int64(maxOutputBytes) {
		return nil, false, errResponseRewriteLimit
	}

	var out bytes.Buffer
	out.Grow(int(finalFrameBytes))
	wroteData := false
	for index, rawLine := range lines {
		if !dataIndexes[index] {
			out.Write(rawLine)
			continue
		}
		if wroteData {
			continue
		}
		out.Write(firstPrefix)
		out.Write(rewritten)
		out.Write(firstEnding)
		wroteData = true
	}
	if out.Len() != int(finalFrameBytes) {
		return nil, false, errResponseRewriteLimit
	}
	return out.Bytes(), true, nil
}

func isBlankSSELine(line []byte) bool {
	content, _ := splitSSELine(line)
	return len(content) == 0
}

func splitSSELine(line []byte) (content, ending []byte) {
	content = line
	if bytes.HasSuffix(content, []byte("\n")) {
		content = content[:len(content)-1]
		ending = []byte("\n")
		if bytes.HasSuffix(content, []byte("\r")) {
			content = content[:len(content)-1]
			ending = []byte("\r\n")
		}
	}
	return content, ending
}

func streamSSEWithToolNameRestore(dst io.Writer, src io.Reader, flusher http.Flusher, aliases map[string]string) error {
	reader := bufio.NewReader(src)
	var frame bytes.Buffer
	for {
		line, err := reader.ReadSlice('\n')
		if len(line) > 0 {
			if frame.Len()+len(line) > maxSSEFrameBytes {
				return writeSSERewriteError(dst, flusher)
			}
			frame.Write(line)
			if isBlankSSELine(line) {
				if writeErr := writeSSEFrame(dst, frame.Bytes(), flusher, aliases, maxSSEFrameBytes); writeErr != nil {
					return writeErr
				}
				frame.Reset()
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF {
				if frame.Len() > 0 {
					return writeSSEFrame(dst, frame.Bytes(), flusher, aliases, maxSSEFrameBytes)
				}
				return nil
			}
			return err
		}
	}
}

func writeSSERewriteError(dst io.Writer, flusher http.Flusher) error {
	responseID := rewriteFailureSequence.Add(1)
	event := fmt.Sprintf("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_responses_compat_error_%d\",\"status\":\"failed\",\"error\":{\"type\":\"server_error\",\"code\":\"adapter_rewrite_failed\",\"message\":\"upstream response could not be safely rewritten\"}}}\n\n", responseID)
	n, err := io.WriteString(dst, event)
	if err != nil {
		return err
	}
	if n != len(event) {
		return io.ErrShortWrite
	}
	if flusher != nil {
		flusher.Flush()
	}
	// The caller returns immediately after this event so the client observes EOF
	// and treats response.failed as the terminal response error.
	return errors.New("SSE event could not be safely rewritten")
}

func writeSSEFrame(dst io.Writer, frame []byte, flusher http.Flusher, aliases map[string]string, maxOutputBytes int) error {
	rewritten, _, err := rewriteSSEFrame(frame, aliases, maxOutputBytes)
	if errors.Is(err, errResponseRewriteLimit) || errors.Is(err, errInvalidJSONResponse) {
		return writeSSERewriteError(dst, flusher)
	}
	if err != nil {
		return err
	}
	n, err := dst.Write(rewritten)
	if err != nil {
		return err
	}
	if n != len(rewritten) {
		return io.ErrShortWrite
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}
