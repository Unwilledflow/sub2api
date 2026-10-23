package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type upstreamErrorFrameReader struct {
	reader  *bufio.Reader
	limit   int64
	pending []byte
	err     error
	// degraded：超限后放弃聚合，改为原字节透传。
	degraded bool
	// passthroughOnLimit 只在流式护栏上开启。护栏是旁挂增强——识别不了财务
	// 错误时必须让数据照常流过，不能因为「帧太大」把一条正常响应变成流错误。
	// 超长非 SSE body（单个巨型 JSON）就是被这里打断的：分类器可以报
	// ErrTooLong，护栏不行。
	passthroughOnLimit bool
}

func newUpstreamErrorFrameReader(reader io.Reader, limit int64) io.Reader {
	return &upstreamErrorFrameReader{reader: bufio.NewReader(reader), limit: limit}
}

// newUpstreamPassthroughFrameReader 与 newUpstreamErrorFrameReader 相同，
// 但超限时降级为原字节透传而不是报错。
func newUpstreamPassthroughFrameReader(reader io.Reader, limit int64) io.Reader {
	return &upstreamErrorFrameReader{reader: bufio.NewReader(reader), limit: limit, passthroughOnLimit: true}
}

// upstreamFinancialFramesReader 把底层 body 包成「按事件聚合」的读取器：
// 多行 data: 拼出来的额度类失败事件会被重写成单行 event: error + data: {...}，
// 这样逐行折叠的逻辑才能识别；非失败事件与正常输出按原字节返回，语义不变。
type upstreamFinancialFramesReader struct {
	reader io.Reader
	closer io.Closer
}

func (r *upstreamFinancialFramesReader) Read(target []byte) (int, error) {
	return r.reader.Read(target)
}

func (r *upstreamFinancialFramesReader) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

// withUpstreamFinancialFrameGuard 给上游 body 装上多行事件聚合，可重复调用。
func withUpstreamFinancialFrameGuard(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return nil
	}
	if _, ok := body.(*upstreamFinancialFramesReader); ok {
		return body
	}
	return &upstreamFinancialFramesReader{reader: newUpstreamPassthroughFrameReader(body, 1<<20), closer: body}
}

func (reader *upstreamErrorFrameReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	if reader.degraded && len(reader.pending) == 0 {
		return reader.reader.Read(target)
	}
	for len(reader.pending) == 0 {
		if reader.err != nil {
			return 0, reader.err
		}
		frame, err := reader.readFrame()
		reader.err = err
		reader.pending = frame
		if len(reader.pending) == 0 && reader.degraded {
			return reader.reader.Read(target)
		}
	}
	count := copy(target, reader.pending)
	reader.pending = reader.pending[count:]
	return count, nil
}

func (reader *upstreamErrorFrameReader) readFrame() ([]byte, error) {
	var frame bytes.Buffer
	var parser openAICompatSSEFrameParser
	dataLines := 0
	plainJSON := false
	lineStart := 0
	for {
		line, err := reader.reader.ReadSlice('\n')
		if int64(frame.Len()+len(line)) > reader.limit {
			if !reader.passthroughOnLimit {
				return nil, bufio.ErrTooLong
			}
			// 降级：把已读到的字节原样交回，后续字节直接透传。
			reader.degraded = true
			if len(line) > 0 {
				_, _ = frame.Write(line)
			}
			return frame.Bytes(), nil
		}
		if frame.Len() == 0 && err != bufio.ErrBufferFull {
			text := strings.TrimRight(string(line), "\r\n")
			if text == "" || strings.HasPrefix(text, ":") {
				return line, err
			}
			if data, ok := extractOpenAISSEDataLine(text); ok && (data == "[DONE]" || json.Valid([]byte(data))) {
				return line, err
			}
		}
		_, _ = frame.Write(line)
		if err == bufio.ErrBufferFull {
			continue
		}
		line = frame.Bytes()[lineStart:]
		lineStart = frame.Len()
		if dataLines == 0 {
			trimmed := bytes.TrimSpace(frame.Bytes())
			plainJSON = bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("["))
		}
		if plainJSON {
			if err != nil || json.Valid(frame.Bytes()) {
				body := frame.Bytes()
				if upstreamFinancialFailureEnvelope(body) {
					var failure any = json.RawMessage(body)
					if !json.Valid(body) {
						failure = string(body)
					}
					payload, _ := json.Marshal(map[string]any{"type": "error", "error": failure})
					return append(append([]byte("data: "), payload...), '\n', '\n'), err
				}
				return body, err
			}
			continue
		}
		text := strings.TrimRight(string(line), "\r\n")
		if frame.Len() == len(line) && strings.HasPrefix(text, ":") {
			return frame.Bytes(), err
		}
		if data, ok := extractOpenAISSEDataLine(text); ok {
			dataLines++
			if dataLines == 1 && (data == "[DONE]" || json.Valid([]byte(data))) {
				return frame.Bytes(), err
			}
		}
		parsed, ready := parser.AddLine(text)
		if err != nil && !ready {
			parsed, ready = parser.Finish()
		}
		if text == "" || err != nil {
			if ready && dataLines > 1 && (upstreamFinancialFailureEnvelope([]byte(parsed.Data)) || parsed.EventType == "error" && IsUpstreamFinancialError(0, []byte(parsed.Data))) {
				var compact bytes.Buffer
				if json.Compact(&compact, []byte(parsed.Data)) == nil {
					return []byte("event: error\ndata: " + compact.String() + "\n\n"), err
				}
			}
			return frame.Bytes(), err
		}
	}
}
