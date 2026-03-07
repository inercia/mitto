//go:build integration

package inprocess

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// minimalPNG is a valid 1x1 pixel red PNG image (67 bytes).
var minimalPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // 8-bit RGB
	0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, // compressed data
	0x00, 0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, // ...
	0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, // IEND chunk
	0x44, 0xae, 0x42, 0x60, 0x82,
}

// TestSendPromptWithImageAndVerifyACPReceivesIt tests the complete image prompt flow:
// upload image via HTTP → send prompt with image_ids via WebSocket → backend loads image,
// base64 encodes it → sends as ContentBlock to ACP → mock ACP server sees the image block.
func TestSendPromptWithImageAndVerifyACPReceivesIt(t *testing.T) {
	ts := SetupTestServer(t)

	// 1. Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// 2. Upload a small test PNG image via HTTP
	imageInfo, err := ts.Client.UploadImage(session.SessionID, "test.png", "image/png", minimalPNG)
	if err != nil {
		t.Fatalf("UploadImage failed: %v", err)
	}
	t.Logf("Uploaded image: id=%s, url=%s, mime=%s", imageInfo.ID, imageInfo.URL, imageInfo.MimeType)

	// 3. Track events
	var (
		mu             sync.Mutex
		connected      bool
		promptComplete bool
		agentMessages  []string
	)

	callbacks := client.SessionCallbacks{
		OnConnected: func(sid, cid, acp string) {
			mu.Lock()
			defer mu.Unlock()
			connected = true
			t.Logf("Connected: session=%s, client=%s", sid, cid)
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			promptComplete = true
			t.Logf("Prompt complete: %d events", eventCount)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			defer mu.Unlock()
			agentMessages = append(agentMessages, html)
			t.Logf("Agent message chunk: %s", html)
		},
	}

	// 4. Connect via WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connection
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connected
	}, "connection")

	// Must send load_events to register as observer
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// 5. Send prompt WITH image_ids
	err = ws.SendPromptWithImages("Describe this image", []string{imageInfo.ID})
	if err != nil {
		t.Fatalf("SendPromptWithImages failed: %v", err)
	}

	// 6. Wait for prompt to complete
	waitFor(t, 15*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete")

	// 7. Verify the mock ACP server received the image block
	mu.Lock()
	defer mu.Unlock()

	fullMessage := strings.Join(agentMessages, "")
	t.Logf("Full agent response: %s", fullMessage)

	if !strings.Contains(fullMessage, "I received 1 image(s)") {
		t.Errorf("Mock ACP did not receive image block.\nExpected response containing 'I received 1 image(s)'\nGot: %s", fullMessage)
	}
	if !strings.Contains(fullMessage, "image/png") {
		t.Errorf("Mock ACP did not receive correct MIME type.\nExpected response containing 'image/png'\nGot: %s", fullMessage)
	}
	if !strings.Contains(fullMessage, "Describe this image") {
		t.Errorf("Mock ACP did not receive the text message.\nExpected response containing 'Describe this image'\nGot: %s", fullMessage)
	}
}
