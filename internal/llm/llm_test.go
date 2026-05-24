package llm

import "testing"

func TestMessageTextConcatenatesOnlyTextParts(t *testing.T) {
	message := Message{
		Role: RoleAssistant,
		Parts: []Part{
			{Type: PartReasoning, Text: "thinking"},
			{Type: PartText, Text: "hel"},
			{Type: PartText, Text: "lo"},
		},
	}

	if got := message.Text(); got != "hello" {
		t.Fatalf("Text() = %q, want hello", got)
	}
}

func TestNewTextMessage(t *testing.T) {
	message := NewTextMessage(RoleUser, "hello")

	if message.Role != RoleUser {
		t.Fatalf("role = %q, want user", message.Role)
	}
	if got := message.Text(); got != "hello" {
		t.Fatalf("text = %q, want hello", got)
	}
}

func TestCloneMessagesDeepCopiesPartsAndSummaries(t *testing.T) {
	messages := []Message{
		{
			Role: RoleAssistant,
			Parts: []Part{
				{
					Type:             PartReasoning,
					Text:             "thinking",
					ID:               "rs_1",
					Summary:          []string{"summary"},
					EncryptedContent: "encrypted",
				},
				{Type: PartText, Text: "answer"},
			},
		},
	}

	clone := CloneMessages(messages)
	clone[0].Parts[0].Summary[0] = "changed"
	clone[0].Parts[0].Text = "changed"
	clone[0].Parts[1].Text = "changed"

	if messages[0].Parts[0].Summary[0] != "summary" {
		t.Fatalf("original summary changed to %q", messages[0].Parts[0].Summary[0])
	}
	if messages[0].Parts[0].Text != "thinking" || messages[0].Parts[1].Text != "answer" {
		t.Fatalf("original parts changed: %#v", messages[0].Parts)
	}
}

func TestCloneMessagesPreservesNil(t *testing.T) {
	if got := CloneMessages(nil); got != nil {
		t.Fatalf("CloneMessages(nil) = %#v, want nil", got)
	}
}

func TestRequestCloneDeepCopiesMessages(t *testing.T) {
	request := Request{
		Model:     "test-model",
		Messages:  []Message{NewTextMessage(RoleUser, "hello")},
		Reasoning: ReasoningOptions{Summary: "auto", Effort: "high"},
	}

	clone := request.Clone()
	clone.Messages[0].Parts[0].Text = "changed"

	if request.Messages[0].Text() != "hello" {
		t.Fatalf("original request message = %q, want hello", request.Messages[0].Text())
	}
	if clone.Model != "test-model" || clone.Reasoning.Effort != "high" {
		t.Fatalf("clone = %#v, want scalar fields preserved", clone)
	}
}
