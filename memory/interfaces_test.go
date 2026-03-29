package memory

import "testing"

func TestEmbedPart_TextOnly(t *testing.T) {
	p := EmbedPart{Text: "hello world"}
	if p.Text != "hello world" {
		t.Errorf("Text = %q, want %q", p.Text, "hello world")
	}
	if len(p.Bytes) != 0 {
		t.Error("Bytes should be empty for text-only part")
	}
	if p.MIMEType != "" {
		t.Error("MIMEType should be empty for text-only part")
	}
}

func TestEmbedPart_Binary(t *testing.T) {
	p := EmbedPart{Bytes: []byte{0xFF, 0xD8}, MIMEType: "image/jpeg"}
	if len(p.Bytes) != 2 {
		t.Errorf("Bytes len = %d, want 2", len(p.Bytes))
	}
	if p.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want %q", p.MIMEType, "image/jpeg")
	}
	if p.Text != "" {
		t.Error("Text should be empty for binary part")
	}
}

// compile-time assertion: ensure EmbedContent is part of the Embedder interface.
var _ Embedder = (embedderWithContent)(nil)

type embedderWithContent interface {
	Embedder
}
