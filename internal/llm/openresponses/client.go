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

	"example.com/llm-chat-web/internal/llm"
)

var ErrStreamFailed = errors.New("openresponses stream failed")

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: http.DefaultClient,
	}
}

func (c *Client) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	body, err := json.Marshal(c.createRequestBody(request))
	if err != nil {
		return nil, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.responsesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("%w: status %d: %s", ErrStreamFailed, response.StatusCode, strings.TrimSpace(string(errorBody)))
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &stream{
		body:    response.Body,
		scanner: scanner,
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
	summary := request.Reasoning.Summary
	if summary == "" {
		summary = "auto"
	}
	if summary != "" {
		reasoning["summary"] = summary
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
	body    io.ReadCloser
	scanner *bufio.Scanner
	data    []string
	done    bool
}

func (s *stream) Next() (llm.Event, error) {
	if s.done {
		return llm.Event{}, io.EOF
	}

	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			event, ok, err := s.dispatch()
			if err != nil || ok {
				return event, err
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			s.data = append(s.data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := s.scanner.Err(); err != nil {
		return llm.Event{}, err
	}

	event, ok, err := s.dispatch()
	if err != nil || ok {
		return event, err
	}
	s.done = true
	return llm.Event{}, io.EOF
}

func (s *stream) Close() error {
	return s.body.Close()
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
	return mapPayload(payload)
}

func mapPayload(payload map[string]any) (llm.Event, bool, error) {
	eventType, _ := payload["type"].(string)
	switch eventType {
	case "response.output_text.delta":
		return llm.Event{
			Type:  llm.EventTextDelta,
			Delta: stringField(payload, "delta"),
		}, true, nil
	case "response.reasoning.delta", "response.reasoning_summary_text.delta":
		return llm.Event{
			Type:  llm.EventReasoningDelta,
			Delta: stringField(payload, "delta"),
		}, true, nil
	case "response.output_item.done":
		part := outputItemPart(payload)
		if part.Type == "" {
			return llm.Event{}, false, nil
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
