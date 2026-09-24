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
	return restoreToolNames(data, aliases, maxOutputBytes, "", true)
}

func restoreToolNamesInSSEJSON(data []byte, aliases map[string]string, maxOutputBytes int, eventType string) ([]byte, bool, error) {
	return restoreToolNames(data, aliases, maxOutputBytes, eventType, false)
}

type toolNameReplacement struct {
	object   map[string]any
	original string
}

func restoreToolNames(data []byte, aliases map[string]string, maxOutputBytes int, eventType string, responseJSON bool) ([]byte, bool, error) {
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

	targets := collectToolNameTargets(response, eventType, responseJSON)
	replacements := make([]toolNameReplacement, 0, len(targets))
	for _, target := range targets {
		name, ok := target["name"].(string)
		if !ok {
			continue
		}
		original, exists := aliases[name]
		if exists && original != name {
			replacements = append(replacements, toolNameReplacement{object: target, original: original})
		}
	}
	if len(replacements) == 0 {
		return data, false, nil
	}

	finalSize, err := encodedJSONSize(response)
	if err != nil {
		return nil, false, err
	}
	for _, replacement := range replacements {
		name := replacement.object["name"].(string)
		finalSize, err = replaceEncodedJSONStringSize(finalSize, name, replacement.original)
		if err != nil {
			return nil, false, err
		}
	}
	if maxOutputBytes < 0 || finalSize > int64(maxOutputBytes) {
		return nil, false, errResponseRewriteLimit
	}

	for _, replacement := range replacements {
		replacement.object["name"] = replacement.original
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

func collectToolNameTargets(value any, eventType string, responseJSON bool) []map[string]any {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	targets := make([]map[string]any, 0)
	if responseJSON {
		rootType, _ := root["type"].(string)
		if rootType == "function_call" {
			appendToolNameTarget(&targets, root)
		}
		if isResponseLifecycleEvent(rootType) {
			if response, ok := root["response"].(map[string]any); ok {
				collectResponseToolNameTargets(response, &targets)
			}
			return targets
		}
		collectResponseToolNameTargets(root, &targets)
		return targets
	}

	if eventType == "" {
		eventType, _ = root["type"].(string)
	}
	switch eventType {
	case "response.output_item.added", "response.output_item.done":
		if item, ok := root["item"]; ok {
			collectFunctionCallTargets(item, &targets)
			collectFunctionDefinitionTargets(item, &targets)
		}
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		appendToolNameTarget(&targets, root)
	default:
		if isResponseLifecycleEvent(eventType) {
			if response, ok := root["response"].(map[string]any); ok {
				collectResponseToolNameTargets(response, &targets)
			}
		}
	}
	return targets
}

func isResponseLifecycleEvent(eventType string) bool {
	switch eventType {
	case "response.created", "response.in_progress", "response.completed", "response.incomplete", "response.failed":
		return true
	default:
		return false
	}
}

func collectResponseToolNameTargets(response map[string]any, targets *[]map[string]any) {
	if output, exists := response["output"]; exists {
		collectFunctionCallTargets(output, targets)
	}
	if tools, exists := response["tools"]; exists {
		collectFunctionDefinitionTargets(tools, targets)
	}
	if namespace, exists := response["namespace"]; exists {
		collectFunctionDefinitionTargets(namespace, targets)
	}
}

func collectFunctionCallTargets(value any, targets *[]map[string]any) {
	switch node := value.(type) {
	case []any:
		for _, item := range node {
			collectFunctionCallTargets(item, targets)
		}
	case map[string]any:
		if node["type"] == "function_call" {
			appendToolNameTarget(targets, node)
		}
	}
}

func collectFunctionDefinitionTargets(value any, targets *[]map[string]any) {
	switch node := value.(type) {
	case []any:
		for _, item := range node {
			collectFunctionDefinitionTargets(item, targets)
		}
	case map[string]any:
		switch node["type"] {
		case "function":
			appendToolNameTarget(targets, node)
		case "namespace":
			if tools, exists := node["tools"]; exists {
				collectFunctionDefinitionTargets(tools, targets)
			}
		}
	}
}

func appendToolNameTarget(targets *[]map[string]any, object map[string]any) {
	if _, ok := object["name"].(string); ok {
		*targets = append(*targets, object)
	}
}

func encodedJSONSize(value any) (int64, error) {
	const maxSize = int64(1<<63 - 1)
	add := func(current, amount int64) (int64, error) {
		if amount < 0 || current > maxSize-amount {
			return 0, errResponseRewriteLimit
		}
		return current + amount, nil
	}

	switch node := value.(type) {
	case nil:
		return 4, nil
	case bool:
		if node {
			return 4, nil
		}
		return 5, nil
	case json.Number:
		return int64(len(node)), nil
	case string:
		return encodedJSONQuotedStringSize(node), nil
	case []any:
		size := int64(2)
		for index, child := range node {
			if index > 0 {
				var err error
				size, err = add(size, 1)
				if err != nil {
					return 0, err
				}
			}
			childSize, err := encodedJSONSize(child)
			if err != nil {
				return 0, err
			}
			size, err = add(size, childSize)
			if err != nil {
				return 0, err
			}
		}
		return size, nil
	case map[string]any:
		size := int64(2)
		index := 0
		for key, child := range node {
			if index > 0 {
				var err error
				size, err = add(size, 1)
				if err != nil {
					return 0, err
				}
			}
			keySize, err := add(encodedJSONQuotedStringSize(key), 1)
			if err != nil {
				return 0, err
			}
			size, err = add(size, keySize)
			if err != nil {
				return 0, err
			}
			childSize, err := encodedJSONSize(child)
			if err != nil {
				return 0, err
			}
			size, err = add(size, childSize)
			if err != nil {
				return 0, err
			}
			index++
		}
		return size, nil
	default:
		return 0, errors.New("unsupported value in Responses JSON")
	}
}

func replaceEncodedJSONStringSize(size int64, previous, replacement string) (int64, error) {
	previousSize := encodedJSONQuotedStringSize(previous)
	replacementSize := encodedJSONQuotedStringSize(replacement)
	const maxSize = int64(1<<63 - 1)
	if replacementSize >= previousSize {
		delta := replacementSize - previousSize
		if size > maxSize-delta {
			return 0, errResponseRewriteLimit
		}
		return size + delta, nil
	}
	delta := previousSize - replacementSize
	if delta > size {
		return 0, errResponseRewriteLimit
	}
	return size - delta, nil
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
