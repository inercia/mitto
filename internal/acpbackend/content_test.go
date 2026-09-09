package acpbackend

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestToNeutralContentBlock_Text(t *testing.T) {
	b, ok := ToNeutralContentBlock(acp.TextBlock("hello"))
	if !ok {
		t.Fatal("expected ok=true for a text block")
	}
	if b.Text == nil || b.Text.Text != "hello" {
		t.Fatalf("expected Text=%q, got %+v", "hello", b.Text)
	}
}

func TestToNeutralContentBlock_Image(t *testing.T) {
	b, ok := ToNeutralContentBlock(acp.ImageBlock("YmFzZTY0", "image/png"))
	if !ok {
		t.Fatal("expected ok=true for an image block")
	}
	if b.Image == nil || b.Image.Data != "YmFzZTY0" || b.Image.MimeType != "image/png" {
		t.Fatalf("unexpected image block: %+v", b.Image)
	}
}

func TestToNeutralContentBlock_ResourceLink(t *testing.T) {
	mime := "text/plain"
	b, ok := ToNeutralContentBlock(acp.ContentBlock{ResourceLink: &acp.ContentBlockResourceLink{Uri: "file:///tmp/a.txt", MimeType: &mime}})
	if !ok {
		t.Fatal("expected ok=true for a resource link")
	}
	if b.File == nil || b.File.Path != "file:///tmp/a.txt" || b.File.MimeType != "text/plain" {
		t.Fatalf("unexpected file block: %+v", b.File)
	}
}

func TestToNeutralContentBlock_ResourceLinkNoMime(t *testing.T) {
	b, ok := ToNeutralContentBlock(acp.ContentBlock{ResourceLink: &acp.ContentBlockResourceLink{Uri: "file:///tmp/a.txt"}})
	if !ok || b.File.MimeType != "" {
		t.Fatalf("expected empty mime type, got %+v (ok=%v)", b.File, ok)
	}
}

func TestToNeutralContentBlock_UnsupportedDropped(t *testing.T) {
	// Audio has no neutral analogue; must be dropped, not lossily coerced.
	if _, ok := ToNeutralContentBlock(acp.ContentBlock{Audio: &acp.ContentBlockAudio{}}); ok {
		t.Fatal("expected ok=false for an audio block (no neutral analogue)")
	}
	if _, ok := ToNeutralContentBlock(acp.ContentBlock{}); ok {
		t.Fatal("expected ok=false for an empty/unrecognized union")
	}
}

func TestToNeutralContentBlocks_SkipsUnsupported(t *testing.T) {
	in := []acp.ContentBlock{
		acp.TextBlock("a"),
		{Audio: &acp.ContentBlockAudio{}},
		acp.TextBlock("b"),
	}
	out := ToNeutralContentBlocks(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 blocks (audio dropped), got %d: %+v", len(out), out)
	}
	if out[0].Text.Text != "a" || out[1].Text.Text != "b" {
		t.Fatalf("unexpected content: %+v", out)
	}
}

func TestFromNeutralContentBlock_RoundTrips(t *testing.T) {
	cases := []agentbackend.ContentBlock{
		{Text: &agentbackend.TextBlock{Text: "hi"}},
		{Image: &agentbackend.ImageBlock{Data: "ZGF0YQ==", MimeType: "image/jpeg"}},
		{File: &agentbackend.FileBlock{Path: "/tmp/x.txt"}},
		{File: &agentbackend.FileBlock{Path: "s3://bucket/x.txt"}},
	}
	for _, in := range cases {
		out, ok := FromNeutralContentBlock(in)
		if !ok {
			t.Fatalf("expected ok=true for %+v", in)
		}
		switch {
		case in.Text != nil:
			if out.Text == nil || out.Text.Text != in.Text.Text {
				t.Errorf("text mismatch: got %+v", out.Text)
			}
		case in.Image != nil:
			if out.Image == nil || out.Image.Data != in.Image.Data || out.Image.MimeType != in.Image.MimeType {
				t.Errorf("image mismatch: got %+v", out.Image)
			}
		case in.File != nil:
			if out.ResourceLink == nil || out.ResourceLink.Name != in.File.Path {
				t.Errorf("file mismatch: got %+v", out.ResourceLink)
			}
		}
	}
}

func TestFromNeutralContentBlock_ZeroValue(t *testing.T) {
	if _, ok := FromNeutralContentBlock(agentbackend.ContentBlock{}); ok {
		t.Fatal("expected ok=false for a zero-value ContentBlock")
	}
}

func TestFromNeutralContentBlocks_SkipsZeroValues(t *testing.T) {
	in := []agentbackend.ContentBlock{
		{Text: &agentbackend.TextBlock{Text: "a"}},
		{},
		{Text: &agentbackend.TextBlock{Text: "b"}},
	}
	out := FromNeutralContentBlocks(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(out))
	}
}

func TestFileURI(t *testing.T) {
	if got := fileURI("/tmp/a.txt"); got != "file:///tmp/a.txt" {
		t.Errorf("expected file:// prefix, got %q", got)
	}
	if got := fileURI("s3://bucket/x"); got != "s3://bucket/x" {
		t.Errorf("expected existing scheme preserved, got %q", got)
	}
}
