package service

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

func ExtractContentModerationText(protocol string, body []byte) string {
	return ExtractContentModerationInput(protocol, body).Text
}

func ExtractContentModerationInput(protocol string, body []byte) ContentModerationInput {
	return extractContentModerationInput(protocol, body, true)
}

// ExtractContentModerationKeywordInput is the local keyword-engine entry point.
// It keeps reminder blocks in the text (keyword checks must see everything a
// client sent) and routes image parts through the metadata/embedded-text audit
// instead of the vision path.
func ExtractContentModerationKeywordInput(protocol string, body []byte) ContentModerationInput {
	return extractContentModerationInputWithMode(protocol, body, false, true)
}

// Keyword checks share semantic moderation's current-user boundaries, but must
// inspect client-supplied reminder blocks as ordinary user text.
func extractContentModerationKeywordText(protocol string, body []byte) string {
	return extractContentModerationInput(protocol, body, false).Text
}

type moderationTextCollector struct {
	filterReminders bool
	// keywordOnly is a local addition, orthogonal to filterReminders: the
	// keyword engine must inspect image *metadata and embedded text* (URLs,
	// SVG, PNG tEXt, JPEG comments) rather than handing the payload to the
	// vision engine as an image.
	keywordOnly bool
}

func extractContentModerationInput(protocol string, body []byte, filterReminders bool) ContentModerationInput {
	return extractContentModerationInputWithMode(protocol, body, filterReminders, false)
}

func extractContentModerationInputWithMode(protocol string, body []byte, filterReminders, keywordOnly bool) ContentModerationInput {
	collector := moderationTextCollector{filterReminders: filterReminders, keywordOnly: keywordOnly}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ContentModerationInput{}
	}
	var parts []string
	var images []string
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		collector.collectLastAnthropicUserMessage(gjson.GetBytes(body, "messages"), &parts, &images)
	case ContentModerationProtocolOpenAIChat:
		collector.collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
	case ContentModerationProtocolOpenAIResponses:
		collector.collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
	case ContentModerationProtocolGemini:
		collector.collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	case ContentModerationProtocolOpenAIImages:
		collector.addModerationText(&parts, gjson.GetBytes(body, "prompt").String())
		collector.collectContentValue(gjson.GetBytes(body, "images"), &parts, &images)
	case ContentModerationProtocolTypeSafeSystemOne:
		// System One carries no client-harness reminder blocks, so a literal
		// <system-reminder> is ordinary user text and must never be skipped.
		moderationTextCollector{keywordOnly: keywordOnly}.collectSystemOneInput(body, &parts)
	default:
		collector.collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
		collector.collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
		collector.collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	}
	out := ContentModerationInput{
		Text:   normalizeContentModerationText(strings.Join(parts, "\n")),
		Images: normalizeModerationImages(images),
	}
	if filterReminders {
		out.Normalize()
	}
	return out
}

// collectSystemOneInput moderates every client-controlled text of a System One
// request: question IDs, every question field except the validated type,
// unknown top-level extension fields, and the evaluated state. Object keys are
// sent to Jev as part of the JSON, so they are moderated like values.
func (collector moderationTextCollector) collectSystemOneInput(body []byte, parts *[]string) {
	root := gjson.ParseBytes(body)
	questions := root.Get("questions")
	if !questions.IsObject() {
		collector.collectSystemOneText(questions, parts)
	}
	questions.ForEach(func(id, question gjson.Result) bool {
		collector.addModerationText(parts, id.String())
		if !question.IsObject() {
			collector.collectSystemOneText(question, parts)
			return true
		}
		question.ForEach(func(field, value gjson.Result) bool {
			switch field.String() {
			case "type":
				return true
			case "instructions", "criteria":
			default:
				collector.addModerationText(parts, field.String())
			}
			collector.collectSystemOneText(value, parts)
			return true
		})
		return true
	})
	root.ForEach(func(field, value gjson.Result) bool {
		switch field.String() {
		case "model", "stream", "state", "questions":
			return true
		}
		collector.addModerationText(parts, field.String())
		collector.collectSystemOneText(value, parts)
		return true
	})
	collector.collectSystemOneText(root.Get("state"), parts)
}

func (collector moderationTextCollector) collectSystemOneText(value gjson.Result, parts *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		collector.addModerationText(parts, value.String())
	case value.IsArray():
		value.ForEach(func(_, child gjson.Result) bool {
			collector.collectSystemOneText(child, parts)
			return true
		})
	case value.IsObject():
		value.ForEach(func(key, child gjson.Result) bool {
			collector.addModerationText(parts, key.String())
			collector.collectSystemOneText(child, parts)
			return true
		})
	}
}

func (collector moderationTextCollector) collectLastRoleMessage(messages gjson.Result, role string, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	if strings.ToLower(strings.TrimSpace(last.Get("role").String())) != role {
		return
	}
	var candidate []string
	var candidateImages []string
	collector.collectContentValue(last.Get("content"), &candidate, &candidateImages)
	if normalizeContentModerationText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func (collector moderationTextCollector) collectLastAnthropicUserMessage(messages gjson.Result, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	if len(array) == 0 {
		return
	}
	// Anthropic requests may append a system message after the user turn. It
	// does not represent a new model/tool turn, so retain the latest user
	// content while continuing to reject assistant/tool-ended loops.
	lastUser := len(array) - 1
	for lastUser >= 0 && strings.ToLower(strings.TrimSpace(array[lastUser].Get("role").String())) == "system" {
		lastUser--
	}
	if lastUser < 0 || strings.ToLower(strings.TrimSpace(array[lastUser].Get("role").String())) != "user" {
		return
	}
	var candidate []string
	var candidateImages []string
	collector.collectAnthropicUserContentValue(array[lastUser].Get("content"), &candidate, &candidateImages)
	if normalizeContentModerationText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func (collector moderationTextCollector) collectAnthropicUserContentValue(value gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		if !collector.filterReminders || !isAnthropicSystemReminderText(value.String()) {
			collector.addModerationText(parts, value.String())
		}
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collector.collectAnthropicUserContentValue(item, parts, images)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() && (!collector.filterReminders || !isAnthropicSystemReminderText(value.Get("text").String())) {
				collector.addModerationText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collector.collectAnthropicUserContentValue(value.Get("content"), parts, images)
			}
		case "image_url", "input_image", "image":
			collector.collectContentValue(value, parts, images)
		}
	}
}

func isAnthropicSystemReminderText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "<system-reminder>")
}

func (collector moderationTextCollector) collectLastResponsesInput(input gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		collector.addModerationText(parts, input.String())
	case input.IsArray():
		array := input.Array()
		if len(array) == 0 {
			return
		}
		last := array[len(array)-1]
		if !collector.isResponsesUserTextItem(last) {
			return
		}
		collector.collectContentValue(last.Get("content"), parts, images)
		if last.Get("type").String() == "input_text" || last.Get("text").Exists() {
			collector.collectContentValue(last, parts, images)
		}
	case input.IsObject():
		if collector.isResponsesUserTextItem(input) {
			collector.collectContentValue(input.Get("content"), parts, images)
			if input.Get("type").String() == "input_text" || input.Get("text").Exists() {
				collector.collectContentValue(input, parts, images)
			}
		}
	}
}

func (collector moderationTextCollector) isResponsesUserTextItem(item gjson.Result) bool {
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	if role == "user" {
		return collector.responseItemHasModerationText(item)
	}
	if role != "" {
		return false
	}
	return collector.responseItemHasModerationText(item)
}

// collectLastResponsesUserTurnForDisplay 只用于风控详情展示：从数组尾部向前
// 回溯到最近一条真正的 user 文本。语义审核仍然只用严格末条判定，避免把旧
// 轮次重新送入审核。
func (collector moderationTextCollector) collectLastResponsesUserTurnForDisplay(input gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		collector.addModerationText(parts, input.String())
	case input.IsArray():
		array := input.Array()
		for i := len(array) - 1; i >= 0; i-- {
			if !collector.isResponsesDisplayUserItem(array[i]) {
				continue
			}
			collector.collectContentValue(array[i].Get("content"), parts, images)
			if array[i].Get("type").String() == "input_text" || array[i].Get("text").Exists() {
				collector.collectContentValue(array[i], parts, images)
			}
			if normalizeContentModerationText(strings.Join(*parts, "\n")) != "" || len(*images) > 0 {
				return
			}
		}
	case input.IsObject():
		if collector.isResponsesDisplayUserItem(input) {
			collector.collectContentValue(input.Get("content"), parts, images)
			if input.Get("type").String() == "input_text" || input.Get("text").Exists() {
				collector.collectContentValue(input, parts, images)
			}
		}
	}
}

// isResponsesDisplayUserItem 判定条目是否可能是用户本轮输入。仅用于展示回溯：
// 明确非 user 的 role 一律跳过；function_call_output / reasoning / summary
// 等非 message 类型即使带 text 字段也不是用户输入。
func (collector moderationTextCollector) isResponsesDisplayUserItem(item gjson.Result) bool {
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	if role == "assistant" || role == "system" || role == "tool" || role == "developer" {
		return false
	}
	typ := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	switch typ {
	case "", "message", "input_text":
	default:
		return false
	}
	return collector.responseItemHasModerationText(item)
}

func (collector moderationTextCollector) responseItemHasModerationText(item gjson.Result) bool {
	var parts []string
	var images []string
	collector.collectContentValue(item.Get("content"), &parts, &images)
	if item.Get("type").String() == "input_text" || item.Get("text").Exists() {
		collector.collectContentValue(item, &parts, &images)
	}
	return normalizeContentModerationText(strings.Join(parts, "\n")) != "" || len(images) > 0
}

func (collector moderationTextCollector) collectLastGeminiContent(contents gjson.Result, parts *[]string, images *[]string) {
	if !contents.IsArray() {
		return
	}
	array := contents.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	role := strings.ToLower(strings.TrimSpace(last.Get("role").String()))
	if role != "" && role != "user" {
		return
	}
	var candidate []string
	var candidateImages []string
	if arr := last.Get("parts"); arr.IsArray() {
		arr.ForEach(func(_, part gjson.Result) bool {
			collector.addModerationText(&candidate, part.Get("text").String())
			if collector.keywordOnly {
				appendImageKeywordAudit(&candidate, part)
			} else {
				addGeminiModerationImage(&candidateImages, part)
			}
			return true
		})
	}
	if normalizeContentModerationText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

// collectUserImages routes image parts to the correct destination for the mode.
// Semantic moderation hands the payload to the vision engine; keyword checks
// never send payloads upstream, so they take the metadata/embedded-text audit
// instead (URL, SVG, PNG tEXt, JPEG comment).
func (collector moderationTextCollector) collectUserImages(parts *[]string, images *[]string, value gjson.Result) {
	if collector.keywordOnly {
		appendImageKeywordAuditValues(parts, value)
		return
	}
	collector.collectContentValue(value, parts, images)
}

func (collector moderationTextCollector) collectContentValue(value gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		collector.addModerationText(parts, value.String())
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collector.collectContentValue(item, parts, images)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		// Keyword checks never ship payloads upstream, so an image part is
		// audited for embedded text rather than collected as an image.
		if !collector.keywordOnly {
			addModerationImage(images, value.Get("image_url.url").String())
			addModerationImage(images, value.Get("image_url").String())
			addModerationImage(images, value.Get("url").String())
			addModerationImageData(images, value.Get("source.media_type").String(), value.Get("source.data").String())
			addModerationImageData(images, value.Get("source.mediaType").String(), value.Get("source.data").String())
			addModerationImageData(images, value.Get("media_type").String(), value.Get("data").String())
			addModerationImageData(images, value.Get("mime_type").String(), value.Get("data").String())
			addModerationImageData(images, value.Get("mimeType").String(), value.Get("data").String())
			addModerationImage(images, value.Get("source.data").String())
			addModerationImage(images, value.Get("data").String())
			addModerationImage(images, value.Get("base64").String())
		}
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() {
				collector.addModerationText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collector.collectContentValue(value.Get("content"), parts, images)
			}
		case "image_url", "input_image", "image":
			if collector.keywordOnly {
				appendImageKeywordAudit(parts, value)
			}
		}
	}
}

func addGeminiModerationImage(images *[]string, part gjson.Result) {
	if inlineData := part.Get("inline_data"); inlineData.IsObject() {
		mimeType := strings.TrimSpace(inlineData.Get("mime_type").String())
		data := strings.TrimSpace(inlineData.Get("data").String())
		if mimeType != "" && data != "" {
			addModerationImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
		}
	}
	if inlineData := part.Get("inlineData"); inlineData.IsObject() {
		mimeType := strings.TrimSpace(inlineData.Get("mimeType").String())
		data := strings.TrimSpace(inlineData.Get("data").String())
		if mimeType != "" && data != "" {
			addModerationImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
		}
	}
	addModerationImage(images, part.Get("file_data.file_uri").String())
	addModerationImage(images, part.Get("fileData.fileUri").String())
}

func addModerationImageData(images *[]string, mimeType string, data string) {
	mimeType = strings.TrimSpace(mimeType)
	data = strings.TrimSpace(data)
	if mimeType == "" || data == "" {
		return
	}
	addModerationImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
}

// ---- Image keyword audit (local customization) ----
const (
	maxImageKeywordAuditBytes = 256 * 1024
	maxImageKeywordRawBytes   = maxImageKeywordAuditBytes * 2
	maxImageKeywordAuditRunes = 2000
)

func appendImageKeywordAuditValues(parts *[]string, value gjson.Result) {
	if !value.Exists() {
		return
	}
	if value.IsArray() {
		value.ForEach(func(_, item gjson.Result) bool {
			appendImageKeywordAudit(parts, item)
			return true
		})
		return
	}
	appendImageKeywordAudit(parts, value)
}

func imageKeywordAuditPresent(value gjson.Result) bool {
	for _, key := range []string{
		"image_url", "image_url.url", "url", "source.url", "source.data",
		"data", "base64", "inline_data", "inlineData",
		"file_uri", "file_data.file_uri", "fileData.fileUri",
	} {
		if value.Get(key).Exists() {
			return true
		}
	}
	return false
}

func appendImageKeywordAudit(parts *[]string, value gjson.Result) {
	if parts == nil || !value.Exists() {
		return
	}
	if value.Type == gjson.String {
		auditImageReference(parts, value)
		return
	}
	if !value.IsObject() {
		return
	}
	for _, key := range []string{
		"image_url.url", "image_url", "url", "source.url", "file_uri",
		"file_data.file_uri", "fileData.fileUri",
	} {
		auditImageReference(parts, value.Get(key))
	}
	for _, key := range []string{"alt", "caption", "filename", "file_name", "name", "title"} {
		if text, ok := boundedGJSONString(value.Get(key), 4096); ok {
			addImageAuditText(parts, text)
		}
	}
	mediaType := firstGJSONString(value, "source.media_type", "source.mediaType", "media_type", "mime_type", "mimeType")
	for _, key := range []string{"source.data", "data", "base64"} {
		if payload, ok := boundedGJSONString(value.Get(key), maxImageKeywordRawBytes); ok {
			auditImagePayload(parts, mediaType, payload)
		}
	}
	if inline := value.Get("inline_data"); inline.IsObject() {
		if payload, ok := boundedGJSONString(inline.Get("data"), maxImageKeywordRawBytes); ok {
			auditImagePayload(parts, inline.Get("mime_type").String(), payload)
		}
	}
	if inline := value.Get("inlineData"); inline.IsObject() {
		if payload, ok := boundedGJSONString(inline.Get("data"), maxImageKeywordRawBytes); ok {
			auditImagePayload(parts, inline.Get("mimeType").String(), payload)
		}
	}
}

func firstGJSONString(value gjson.Result, keys ...string) string {
	for _, key := range keys {
		if text := strings.TrimSpace(value.Get(key).String()); text != "" {
			return text
		}
	}
	return ""
}

func boundedGJSONString(value gjson.Result, maxBytes int) (string, bool) {
	if value.Type != gjson.String || maxBytes <= 0 {
		return "", false
	}
	if len(value.Raw) > maxBytes+2 {
		return "", false
	}
	return value.String(), true
}

func auditImageReference(parts *[]string, value gjson.Result) {
	text, ok := boundedGJSONString(value, maxImageKeywordRawBytes)
	if !ok {
		return
	}
	addImageReferenceKeywordText(parts, text)
	auditImagePayload(parts, "", text)
}

func addImageReferenceKeywordText(parts *[]string, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") {
		return
	}
	if len(raw) > maxImageKeywordRawBytes || looksLikeBase64(raw) {
		return
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			addImageAuditText(parts, trimRunes(raw, maxImageKeywordAuditRunes))
			return
		}
		path, err := url.PathUnescape(parsed.EscapedPath())
		if err != nil {
			path = parsed.EscapedPath()
		}
		query, err := url.QueryUnescape(parsed.RawQuery)
		if err != nil {
			query = parsed.RawQuery
		}
		addImageAuditText(parts, trimRunes(strings.TrimSpace(parsed.Host+" "+path+" "+query), maxImageKeywordAuditRunes))
		return
	}
	addImageAuditText(parts, trimRunes(raw, maxImageKeywordAuditRunes))
}

func looksLikeBase64(value string) bool {
	if len(value) < 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/', c == '=', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func auditImagePayload(parts *[]string, mediaType string, payload string) {
	decoded, decodedType := decodeBoundedImagePayload(mediaType, payload)
	if len(decoded) == 0 {
		return
	}
	if decodedType != "" {
		mediaType = decodedType
	}
	addImageAuditText(parts, trimRunes(extractEmbeddedImageText(mediaType, decoded), maxImageKeywordAuditRunes))
}

func decodeBoundedImagePayload(mediaType string, payload string) ([]byte, string) {
	payload = strings.TrimSpace(payload)
	if payload == "" || strings.HasPrefix(payload, "http://") || strings.HasPrefix(payload, "https://") {
		return nil, ""
	}
	if len(payload) > maxImageKeywordRawBytes {
		return nil, ""
	}
	if strings.HasPrefix(strings.ToLower(payload), "data:") {
		comma := strings.IndexByte(payload, ',')
		if comma < 0 {
			return nil, ""
		}
		meta := payload[len("data:"):comma]
		body := payload[comma+1:]
		if semi := strings.IndexByte(meta, ';'); semi >= 0 {
			if mediaType == "" {
				mediaType = meta[:semi]
			}
			meta = meta[semi+1:]
		} else if mediaType == "" {
			mediaType = meta
			meta = ""
		}
		if strings.EqualFold(strings.TrimSpace(meta), "base64") || strings.Contains(strings.ToLower(payload[:comma]), ";base64") {
			return decodeBoundedBase64(body), mediaType
		}
		decoded, err := url.PathUnescape(body)
		if err != nil {
			decoded = body
		}
		return limitImageAuditBytes([]byte(decoded)), mediaType
	}
	if !shouldDecodeImageMedia(mediaType) && mediaType != "" {
		return nil, mediaType
	}
	return decodeBoundedBase64(payload), mediaType
}

func shouldDecodeImageMedia(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "" || strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "svg") || strings.Contains(mediaType, "xml")
}

func decodeBoundedBase64(payload string) []byte {
	payload = strings.TrimSpace(payload)
	if payload == "" || len(payload) > maxImageKeywordRawBytes {
		return nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return limitImageAuditBytes(decoded)
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(payload); err == nil {
		return limitImageAuditBytes(decoded)
	}
	return nil
}

func limitImageAuditBytes(decoded []byte) []byte {
	if len(decoded) > maxImageKeywordAuditBytes {
		return decoded[:maxImageKeywordAuditBytes]
	}
	return decoded
}

func extractEmbeddedImageText(mediaType string, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var b strings.Builder
	lowerType := strings.ToLower(mediaType)
	if strings.Contains(lowerType, "svg") || strings.Contains(lowerType, "xml") || strings.HasPrefix(lowerType, "text/") || looksLikeTextPayload(data) {
		if utf8.Valid(data) {
			b.Write(data)
		}
	}
	writeChunkText(&b, extractPNGText(data))
	writeChunkText(&b, extractJPEGComments(data))
	return b.String()
}

func looksLikeTextPayload(data []byte) bool {
	if len(data) == 0 || !utf8.Valid(data) {
		return false
	}
	switch data[0] {
	case '<', '{':
		return true
	default:
		return false
	}
}

func extractPNGText(data []byte) string {
	if len(data) < 8 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return ""
	}
	var b strings.Builder
	offset := 8
	for offset+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length < 0 || offset+8+length > len(data) {
			break
		}
		chunkType := string(data[offset+4 : offset+8])
		chunk := data[offset+8 : offset+8+length]
		switch chunkType {
		case "tEXt":
			if zero := bytes.IndexByte(chunk, 0); zero >= 0 && zero+1 < len(chunk) {
				writeChunkText(&b, string(chunk[zero+1:]))
			}
		case "zTXt":
			if zero := bytes.IndexByte(chunk, 0); zero >= 0 && zero+2 < len(chunk) && chunk[zero+1] == 0 {
				writeChunkText(&b, string(inflateBounded(chunk[zero+2:])))
			}
		case "iTXt":
			writeChunkText(&b, extractITXt(chunk))
		}
		offset += 12 + length
	}
	return b.String()
}

func extractITXt(chunk []byte) string {
	zero := bytes.IndexByte(chunk, 0)
	if zero < 0 || zero+2 >= len(chunk) {
		return ""
	}
	rest := chunk[zero+1:]
	compressed := rest[0] != 0
	rest = rest[2:]
	for i := 0; i < 2; i++ {
		next := bytes.IndexByte(rest, 0)
		if next < 0 {
			return ""
		}
		rest = rest[next+1:]
	}
	if compressed {
		return string(inflateBounded(rest))
	}
	return string(rest)
}

func extractJPEGComments(data []byte) string {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return ""
	}
	var b strings.Builder
	offset := 2
	for offset+4 <= len(data) {
		if data[offset] != 0xFF {
			break
		}
		for offset < len(data) && data[offset] == 0xFF {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xD9 || marker == 0xDA {
			break
		}
		if marker >= 0xD0 && marker <= 0xD9 {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || offset+length > len(data) {
			break
		}
		if marker == 0xFE {
			writeChunkText(&b, string(data[offset+2:offset+length]))
		}
		offset += length
	}
	return b.String()
}

func writeChunkText(b *strings.Builder, text string) {
	if b == nil || !utf8.ValidString(text) {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(text)
}

func inflateBounded(data []byte) []byte {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer reader.Close()
	out, err := io.ReadAll(io.LimitReader(reader, maxImageKeywordAuditBytes))
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		if len(out) == 0 {
			return nil
		}
	}
	return out
}

func addModerationImage(images *[]string, image string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return
	}
	if strings.HasPrefix(image, "data:") || strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		*images = append(*images, image)
	}
}

func normalizeModerationImages(images []string) []string {
	out := make([]string, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		out = append(out, image)
	}
	return out
}

func limitContentModerationImages(images []string) []string {
	if len(images) <= maxContentModerationInputImages {
		return images
	}
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(images))))
	if err != nil {
		return images[:maxContentModerationInputImages]
	}
	return []string{images[int(idx.Int64())]}
}

func (collector moderationTextCollector) addModerationText(parts *[]string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if collector.filterReminders && strings.Contains(text, "<system-reminder>") {
		return
	}
	*parts = append(*parts, text)
}

// addImageAuditText appends text derived from an image part (URL, embedded
// tEXt/comment, decoded EXIF-ish payload) to the keyword corpus. It is
// deliberately not reminder-filtered: the bytes come from an image payload, so
// a literal "<system-reminder>" inside them is attacker-controlled data that
// keyword moderation must still see.
func addImageAuditText(parts *[]string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	*parts = append(*parts, text)
}

func normalizeContentModerationText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

// ExtractContentModerationFullText keeps every client-controlled user text turn.
// It is used for audit excerpts and must include all user content, unlike the
// last-turn boundary used by semantic moderation.
func ExtractContentModerationFullText(protocol string, body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	collector := moderationTextCollector{}
	var parts []string
	var images []string
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		collector.collectAllRoleMessages(gjson.GetBytes(body, "messages"), "user", &parts, &images)
	case ContentModerationProtocolOpenAIChat:
		collector.collectAllRoleMessages(gjson.GetBytes(body, "messages"), "user", &parts, &images)
	case ContentModerationProtocolOpenAIResponses:
		collector.collectAllResponsesUserInput(gjson.GetBytes(body, "input"), &parts, &images)
	case ContentModerationProtocolGemini:
		collector.collectAllGeminiUserContent(gjson.GetBytes(body, "contents"), &parts, &images)
	case ContentModerationProtocolOpenAIImages:
		collector.addModerationText(&parts, gjson.GetBytes(body, "prompt").String())
	default:
		collector.collectAllResponsesUserInput(gjson.GetBytes(body, "input"), &parts, &images)
		collector.collectAllRoleMessages(gjson.GetBytes(body, "messages"), "user", &parts, &images)
		collector.collectAllGeminiUserContent(gjson.GetBytes(body, "contents"), &parts, &images)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// ExtractContentModerationLastUserTurn returns the client's latest user turn,
// parsed out of the request body, for display in the moderation log.
//
// It intentionally mirrors the last-turn boundary used by semantic moderation
// instead of concatenating every historical user message: an operator reading a
// block wants the text that actually triggered it, not a wall of earlier turns.
func ExtractContentModerationLastUserTurn(protocol string, body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	collector := moderationTextCollector{filterReminders: true}
	var parts []string
	var images []string
	// Responses 详情展示需要容忍尾部工具回包/reasoning/summary：运维要看的是
	// 触发本轮的用户输入，而不是因为尾部不是 user 就只剩上游错误。
	if protocol == ContentModerationProtocolOpenAIResponses {
		collector.collectLastResponsesUserTurnForDisplay(gjson.GetBytes(body, "input"), &parts, &images)
		if text := strings.TrimSpace(strings.Join(parts, "\n\n")); text != "" {
			return text
		}
		if len(images) > 0 {
			return "[" + strings.Join(images, ", ") + "]"
		}
		// 回溯不到 user 条目时，保留旧行为：只认严格末条，避免误取历史文本。
		collector.collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
		if text := strings.TrimSpace(strings.Join(parts, "\n\n")); text != "" {
			return text
		}
		if len(images) > 0 {
			return "[" + strings.Join(images, ", ") + "]"
		}
	}
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		collector.collectLastAnthropicUserMessage(gjson.GetBytes(body, "messages"), &parts, &images)
	case ContentModerationProtocolOpenAIChat:
		collector.collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
	case ContentModerationProtocolOpenAIResponses:
		collector.collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
	case ContentModerationProtocolGemini:
		collector.collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	case ContentModerationProtocolOpenAIImages:
		collector.addModerationText(&parts, gjson.GetBytes(body, "prompt").String())
	default:
		collector.collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
		collector.collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
		collector.collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	}
	text := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if text == "" && len(images) > 0 {
		// A turn made only of images still has to show something useful.
		return "[" + strings.Join(images, ", ") + "]"
	}
	return text
}

func (collector moderationTextCollector) collectAllRoleMessages(messages gjson.Result, role string, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	want := strings.ToLower(strings.TrimSpace(role))
	for _, item := range messages.Array() {
		if strings.ToLower(strings.TrimSpace(item.Get("role").String())) != want {
			continue
		}
		collector.collectContentValue(item.Get("content"), parts, images)
	}
}

func (collector moderationTextCollector) collectAllResponsesUserInput(input gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		collector.addModerationText(parts, input.String())
	case input.IsArray():
		for _, item := range input.Array() {
			if !collector.isResponsesUserTextItem(item) {
				continue
			}
			collector.collectContentValue(item.Get("content"), parts, images)
			if item.Get("type").String() == "input_text" || item.Get("text").Exists() {
				collector.collectContentValue(item, parts, images)
			}
		}
	case input.IsObject():
		if collector.isResponsesUserTextItem(input) {
			collector.collectContentValue(input.Get("content"), parts, images)
			if input.Get("type").String() == "input_text" || input.Get("text").Exists() {
				collector.collectContentValue(input, parts, images)
			}
		}
	}
}

func (collector moderationTextCollector) collectAllGeminiUserContent(contents gjson.Result, parts *[]string, images *[]string) {
	if !contents.IsArray() {
		return
	}
	for _, item := range contents.Array() {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role != "" && role != "user" {
			continue
		}
		if arr := item.Get("parts"); arr.IsArray() {
			arr.ForEach(func(_, part gjson.Result) bool {
				collector.addModerationText(parts, part.Get("text").String())
				return true
			})
		}
	}
}
