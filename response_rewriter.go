package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"unicode/utf8"
)

const (
	maxResponseRewriteBytes = 64 << 20
	maxSSEFrameBytes        = 64 << 20
)

var (
	errResponseRewriteLimit = errors.New("rewritten response exceeds size limit")
	errInvalidJSONResponse  = errors.New("invalid JSON response")
)

var rewriteFailureSequence atomic.Uint64

func restoreToolNamesInJSON(data []byte, aliases map[string]string, maxOutputBytes int) ([]byte, bool, error) {
	if len(aliases) == 0 {
		return data, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var response any
	if err := decoder.Decode(&response); err != nil {
		return nil, false, errInvalidJSONResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false, errInvalidJSONResponse
	}
	_, changed, err := restoredJSONEncodedSize(response, "", aliases, int64(maxOutputBytes))
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return data, false, nil
	}
	if !restoreAliasedToolNames(response, aliases) {
		return data, false, nil
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return nil, false, err
	}
	encoded := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if len(encoded) > maxOutputBytes {
		return nil, false, errResponseRewriteLimit
	}
	return encoded, true, nil
}

// restoredJSONEncodedSize preflights compact JSON output so alias expansion cannot grow an unbounded buffer.
func restoredJSONEncodedSize(value any, fieldName string, aliases map[string]string, limit int64) (int64, bool, error) {
	add := func(current, amount int64) (int64, error) {
		if amount < 0 || amount > limit || current > limit-amount {
			return 0, errResponseRewriteLimit
		}
		return current + amount, nil
	}

	switch node := value.(type) {
	case nil:
		return 4, false, nil
	case bool:
		if node {
			return 4, false, nil
		}
		return 5, false, nil
	case json.Number:
		return int64(len(node)), false, nil
	case string:
		changed := false
		if fieldName == "name" {
			if original, exists := aliases[node]; exists {
				node = original
				changed = true
			}
		}
		size := encodedJSONQuotedStringSize(node)
		if size > limit {
			return 0, changed, errResponseRewriteLimit
		}
		return size, changed, nil
	case []any:
		size := int64(2)
		changed := false
		for index, child := range node {
			if index > 0 {
				var err error
				size, err = add(size, 1)
				if err != nil {
					return 0, changed, err
				}
			}
			childSize, childChanged, err := restoredJSONEncodedSize(child, "", aliases, limit)
			if err != nil {
				return 0, changed || childChanged, err
			}
			size, err = add(size, childSize)
			if err != nil {
				return 0, changed || childChanged, err
			}
			changed = changed || childChanged
		}
		return size, changed, nil
	case map[string]any:
		size := int64(2)
		changed := false
		index := 0
		for key, child := range node {
			if index > 0 {
				var err error
				size, err = add(size, 1)
				if err != nil {
					return 0, changed, err
				}
			}
			keySize := encodedJSONQuotedStringSize(key) + 1
			var err error
			size, err = add(size, keySize)
			if err != nil {
				return 0, changed, err
			}
			childSize, childChanged, err := restoredJSONEncodedSize(child, key, aliases, limit)
			if err != nil {
				return 0, changed || childChanged, err
			}
			size, err = add(size, childSize)
			if err != nil {
				return 0, changed || childChanged, err
			}
			changed = changed || childChanged
			index++
		}
		return size, changed, nil
	default:
		return 0, false, errors.New("unsupported value in Muse JSON response")
	}
}

// encodedJSONQuotedStringSize matches encoding/json string quoting with HTML escaping disabled.
func encodedJSONQuotedStringSize(value string) int64 {
	size := int64(2)
	for len(value) > 0 {
		r, width := utf8.DecodeRuneInString(value)
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size += 2
		case 0x2028, 0x2029:
			size += 6
		default:
			if r < 0x20 {
				size += 6
			} else if r == utf8.RuneError && width == 1 {
				size += 3
			} else {
				size += int64(width)
			}
		}
		value = value[width:]
	}
	return size
}

func restoreAliasedToolNames(value any, aliases map[string]string) bool {
	changed := false
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			changed = restoreAliasedToolNames(child, aliases) || changed
		}
	case map[string]any:
		if name, ok := node["name"].(string); ok {
			if original, exists := aliases[name]; exists {
				node["name"] = original
				changed = true
			}
		}
		for _, child := range node {
			changed = restoreAliasedToolNames(child, aliases) || changed
		}
	}
	return changed
}

func rewriteSSEFrame(frame []byte, aliases map[string]string, maxOutputBytes int) ([]byte, bool, error) {
	if len(frame) == 0 || len(aliases) == 0 {
		return frame, false, nil
	}
	lines := bytes.SplitAfter(frame, []byte("\n"))
	dataIndexes := make(map[int]bool)
	data := make([][]byte, 0, 1)
	firstPrefix := []byte(nil)
	firstEnding := []byte(nil)
	for index, rawLine := range lines {
		content, ending := splitSSELine(rawLine)
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
	rewritten, changed, err := restoreToolNamesInJSON(joinedData, aliases, maxOutputBytes)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return frame, false, nil
	}
	var out bytes.Buffer
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
	event := fmt.Sprintf("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_muse_adapter_error_%d\",\"status\":\"failed\",\"error\":{\"type\":\"server_error\",\"code\":\"adapter_rewrite_failed\",\"message\":\"Muse response could not be safely rewritten\"}}}\n\n", responseID)
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
