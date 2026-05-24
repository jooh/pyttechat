package openresponses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"example.com/llm-chat-web/internal/llm"
)

var ErrStreamFailed = errors.New("openresponses stream failed")

const DefaultTimeout = 5 * time.Minute

type Client struct {
	baseURL     string
	httpClient  *http.Client
	bearerToken string
}

var marshalJSON = json.Marshal

type Options struct {
	Timeout     time.Duration
	BearerToken string
}

func NewClient(baseURL string) *Client {
	return NewClientWithTimeout(baseURL, DefaultTimeout)
}

func NewClientWithTimeout(baseURL string, timeout time.Duration) *Client {
	return NewClientWithOptions(baseURL, Options{Timeout: timeout})
}

func NewClientWithOptions(baseURL string, opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		httpClient:  &http.Client{Timeout: timeout},
		bearerToken: strings.TrimSpace(opts.BearerToken),
	}
}

func (c *Client) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	body, err := marshalJSON(c.createRequestBody(request))
	if err != nil {
		return nil, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.responsesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("Content-Type", "application/json")
	if c.bearerToken != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("%w: status %d: %s", ErrStreamFailed, response.StatusCode, strings.TrimSpace(string(errorBody)))
	}

	return &stream{
		body:     response.Body,
		reader:   bufio.NewReader(response.Body),
		textSeen: map[string]bool{},
	}, nil
}

func (c *Client) responsesURL() string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return c.baseURL + "/v1/responses"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/responses"
	return parsed.String()
}

func (c *Client) createRequestBody(request llm.Request) map[string]any {
	body := map[string]any{
		"stream":  true,
		"store":   false,
		"include": []string{"reasoning.encrypted_content"},
		"input":   openResponsesInput(request.Messages),
	}
	if request.Model != "" {
		body["model"] = request.Model
	}

	reasoning := map[string]any{}
	if request.Reasoning.Summary != "" {
		reasoning["summary"] = request.Reasoning.Summary
	}
	if request.Reasoning.Effort != "" {
		reasoning["effort"] = request.Reasoning.Effort
	}
	if len(reasoning) > 0 {
		body["reasoning"] = reasoning
	}

	return body
}

func openResponsesInput(messages []llm.Message) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == llm.RoleAssistant {
			for _, part := range message.Parts {
				switch part.Type {
				case llm.PartReasoning:
					input = append(input, reasoningInput(part))
				case llm.PartText:
					if part.Text != "" {
						input = append(input, messageInput(message.Role, part.Text))
					}
				}
			}
			continue
		}

		text := message.Text()
		if text == "" {
			continue
		}
		input = append(input, messageInput(message.Role, text))
	}
	return input
}

func messageInput(role llm.Role, text string) map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    string(role),
		"content": text,
	}
}

func reasoningInput(part llm.Part) map[string]any {
	item := map[string]any{
		"type":    "reasoning",
		"summary": reasoningSummary(part.Summary),
	}
	if part.ID != "" {
		item["id"] = part.ID
	}
	if part.EncryptedContent != "" {
		item["encrypted_content"] = part.EncryptedContent
	}
	return item
}

func reasoningSummary(summary []string) []map[string]string {
	out := make([]map[string]string, 0, len(summary))
	for _, text := range summary {
		out = append(out, map[string]string{
			"type": "summary_text",
			"text": text,
		})
	}
	return out
}

type stream struct {
	body     io.ReadCloser
	reader   *bufio.Reader
	data     []string
	done     bool
	textSeen map[string]bool
}

func (s *stream) Next() (llm.Event, error) {
	if s.done {
		return llm.Event{}, io.EOF
	}

	for {
		line, err := s.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				event, ok, dispatchErr := s.dispatch()
				if dispatchErr != nil || ok {
					return event, dispatchErr
				}
				s.done = true
				return llm.Event{}, io.EOF
			}
			return llm.Event{}, err
		}

		if line == "" {
			event, ok, err := s.dispatch()
			if err != nil || ok {
				return event, err
			}
			continue
		}

		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			s.data = append(s.data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

func (s *stream) Close() error {
	return s.body.Close()
}

func (s *stream) readLine() (string, error) {
	line, err := s.reader.ReadString('\n')
	if err != nil {
		if !errors.Is(err, io.EOF) || line == "" {
			return "", err
		}
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func (s *stream) dispatch() (llm.Event, bool, error) {
	if len(s.data) == 0 {
		return llm.Event{}, false, nil
	}
	raw := strings.Join(s.data, "\n")
	s.data = nil

	if raw == "[DONE]" {
		s.done = true
		return llm.Event{}, false, io.EOF
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return llm.Event{}, false, err
	}
	return s.mapPayload(payload)
}

func (s *stream) mapPayload(payload map[string]any) (llm.Event, bool, error) {
	eventType, _ := payload["type"].(string)
	switch eventType {
	case "response.output_text.delta":
		s.markTextSeen(contentKey(payload))
		return llm.Event{
			Type:  llm.EventTextDelta,
			Delta: stringField(payload, "delta"),
		}, true, nil
	case "response.output_text.done":
		key := contentKey(payload)
		if s.textSeen[key] {
			return llm.Event{}, false, nil
		}
		text := stringField(payload, "text")
		if text == "" {
			return llm.Event{}, false, nil
		}
		s.markTextSeen(key)
		return llm.Event{
			Type:  llm.EventTextDelta,
			Delta: text,
		}, true, nil
	case "response.reasoning.delta", "response.reasoning_summary_text.delta":
		return llm.Event{
			Type:  llm.EventReasoningDelta,
			Delta: stringField(payload, "delta"),
		}, true, nil
	case "response.output_item.done":
		part := outputItemPart(payload)
		if part.Type == "" {
			text := s.missingMessageText(payload)
			if text == "" {
				return llm.Event{}, false, nil
			}
			return llm.Event{Type: llm.EventTextDelta, Delta: text}, true, nil
		}
		return llm.Event{Type: llm.EventOutputItemDone, Part: part}, true, nil
	case "response.completed":
		return completedEvent(payload), true, nil
	case "response.failed", "error":
		return llm.Event{}, false, streamError(payload)
	default:
		return llm.Event{}, false, nil
	}
}

func (s *stream) missingMessageText(payload map[string]any) string {
	item, _ := payload["item"].(map[string]any)
	if item["type"] != "message" || item["role"] != string(llm.RoleAssistant) {
		return ""
	}

	rawContent, _ := item["content"].([]any)
	outputIndex := intField(payload, "output_index")
	itemID := stringField(item, "id")
	var text string
	for i, value := range rawContent {
		part, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if part["type"] != "output_text" && part["type"] != "text" {
			continue
		}
		partText := stringField(part, "text")
		if partText == "" {
			continue
		}
		key := textContentKey(itemID, outputIndex, i)
		if s.textSeen[key] {
			continue
		}
		s.markTextSeen(key)
		text += partText
	}
	return text
}

func (s *stream) markTextSeen(key string) {
	if s.textSeen == nil {
		s.textSeen = map[string]bool{}
	}
	s.textSeen[key] = true
}

func contentKey(payload map[string]any) string {
	return textContentKey(stringField(payload, "item_id"), intField(payload, "output_index"), intField(payload, "content_index"))
}

func textContentKey(itemID string, outputIndex, contentIndex int) string {
	return fmt.Sprintf("%s/%d/%d", itemID, outputIndex, contentIndex)
}

func outputItemPart(payload map[string]any) llm.Part {
	item, _ := payload["item"].(map[string]any)
	if item["type"] != "reasoning" {
		return llm.Part{}
	}
	return llm.Part{
		Type:             llm.PartReasoning,
		ID:               stringField(item, "id"),
		Text:             reasoningText(item),
		Summary:          reasoningSummaryText(item),
		EncryptedContent: stringField(item, "encrypted_content"),
	}
}

func completedEvent(payload map[string]any) llm.Event {
	response, _ := payload["response"].(map[string]any)
	return llm.Event{
		Type:       llm.EventCompleted,
		ResponseID: stringField(response, "id"),
		Usage:      usage(response),
	}
}

func usage(response map[string]any) *llm.Usage {
	rawUsage, ok := response["usage"].(map[string]any)
	if !ok {
		return nil
	}
	usage := &llm.Usage{
		InputTokens:  intField(rawUsage, "input_tokens"),
		OutputTokens: intField(rawUsage, "output_tokens"),
		TotalTokens:  intField(rawUsage, "total_tokens"),
	}
	if details, ok := rawUsage["output_tokens_details"].(map[string]any); ok {
		usage.ReasoningTokens = intField(details, "reasoning_tokens")
	}
	return usage
}

func reasoningText(item map[string]any) string {
	return textFromContentArray(item["content"])
}

func reasoningSummaryText(item map[string]any) []string {
	raw, ok := item["summary"].([]any)
	if !ok {
		return nil
	}
	summary := make([]string, 0, len(raw))
	for _, value := range raw {
		part, ok := value.(map[string]any)
		if !ok {
			continue
		}
		text := stringField(part, "text")
		if text != "" {
			summary = append(summary, text)
		}
	}
	return summary
}

func textFromContentArray(value any) string {
	raw, ok := value.([]any)
	if !ok {
		return ""
	}
	var text string
	for _, item := range raw {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text += stringField(part, "text")
	}
	return text
}

func streamError(payload map[string]any) error {
	message := "stream failed"
	if errorValue, ok := payload["error"].(map[string]any); ok {
		if text := stringField(errorValue, "message"); text != "" {
			message = text
		}
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if errorValue, ok := response["error"].(map[string]any); ok {
			if text := stringField(errorValue, "message"); text != "" {
				message = text
			}
		}
	}
	return fmt.Errorf("%w: %s", ErrStreamFailed, message)
}

func stringField(payload map[string]any, name string) string {
	value, _ := payload[name].(string)
	return value
}

func intField(payload map[string]any, name string) int {
	switch value := payload[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}
