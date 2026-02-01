package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestQueue_AddAndList(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// Initially empty
	messages, err := q.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("List() = %d messages, want 0", len(messages))
	}

	// Add first message (0 = no limit)
	msg1, err := q.Add("Hello", nil, "client1", 0)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if msg1.ID == "" {
		t.Error("Add() returned empty ID")
	}
	if msg1.Message != "Hello" {
		t.Errorf("Add() message = %q, want %q", msg1.Message, "Hello")
	}
	if msg1.ClientID != "client1" {
		t.Errorf("Add() clientID = %q, want %q", msg1.ClientID, "client1")
	}

	// Add second message with images
	msg2, err := q.Add("World", []string{"img1", "img2"}, "client2", 0)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if len(msg2.ImageIDs) != 2 {
		t.Errorf("Add() imageIDs = %v, want 2 items", msg2.ImageIDs)
	}

	// List should return both in FIFO order
	messages, err = q.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("List()[0].ID = %q, want %q", messages[0].ID, msg1.ID)
	}
	if messages[1].ID != msg2.ID {
		t.Errorf("List()[1].ID = %q, want %q", messages[1].ID, msg2.ID)
	}
}

func TestQueue_Get(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg, err := q.Add("Test message", nil, "", 0)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	// Get existing message
	got, err := q.Get(msg.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("Get() ID = %q, want %q", got.ID, msg.ID)
	}
	if got.Message != msg.Message {
		t.Errorf("Get() Message = %q, want %q", got.Message, msg.Message)
	}

	// Get non-existent message
	_, err = q.Get("nonexistent")
	if err != ErrMessageNotFound {
		t.Errorf("Get(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_Remove(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg1, _ := q.Add("First", nil, "", 0)
	msg2, _ := q.Add("Second", nil, "", 0)
	msg3, _ := q.Add("Third", nil, "", 0)

	// Remove middle message
	if err := q.Remove(msg2.ID); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	// Verify it's gone
	messages, _ := q.List()
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg1.ID || messages[1].ID != msg3.ID {
		t.Error("Remove() did not remove the correct message")
	}

	// Remove non-existent message
	err := q.Remove("nonexistent")
	if err != ErrMessageNotFound {
		t.Errorf("Remove(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_UpdateTitle(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg1, _ := q.Add("First message", nil, "", 0)
	msg2, _ := q.Add("Second message", nil, "", 0)

	// Update title of first message
	if err := q.UpdateTitle(msg1.ID, "First Title"); err != nil {
		t.Fatalf("UpdateTitle() error = %v", err)
	}

	// Verify title was updated
	updated, err := q.Get(msg1.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if updated.Title != "First Title" {
		t.Errorf("UpdateTitle() title = %q, want %q", updated.Title, "First Title")
	}

	// Verify second message title is still empty
	msg2Updated, _ := q.Get(msg2.ID)
	if msg2Updated.Title != "" {
		t.Errorf("Second message title = %q, want empty", msg2Updated.Title)
	}

	// Update non-existent message
	err = q.UpdateTitle("nonexistent", "Some Title")
	if err != ErrMessageNotFound {
		t.Errorf("UpdateTitle(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_Clear(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	q.Add("First", nil, "", 0)
	q.Add("Second", nil, "", 0)
	q.Add("Third", nil, "", 0)

	if err := q.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	messages, _ := q.List()
	if len(messages) != 0 {
		t.Errorf("List() after Clear() = %d messages, want 0", len(messages))
	}
}

func TestQueue_Pop(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// Pop from empty queue
	_, err := q.Pop()
	if err != ErrQueueEmpty {
		t.Errorf("Pop() on empty queue error = %v, want ErrQueueEmpty", err)
	}

	msg1, _ := q.Add("First", nil, "", 0)
	msg2, _ := q.Add("Second", nil, "", 0)

	// Pop first message
	popped, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if popped.ID != msg1.ID {
		t.Errorf("Pop() ID = %q, want %q", popped.ID, msg1.ID)
	}

	// Pop second message
	popped, err = q.Pop()
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if popped.ID != msg2.ID {
		t.Errorf("Pop() ID = %q, want %q", popped.ID, msg2.ID)
	}

	// Queue should be empty now
	_, err = q.Pop()
	if err != ErrQueueEmpty {
		t.Errorf("Pop() on empty queue error = %v, want ErrQueueEmpty", err)
	}
}

func TestQueue_Len(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	length, err := q.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if length != 0 {
		t.Errorf("Len() = %d, want 0", length)
	}

	q.Add("First", nil, "", 0)
	q.Add("Second", nil, "", 0)

	length, err = q.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if length != 2 {
		t.Errorf("Len() = %d, want 2", length)
	}
}

func TestQueue_IsEmpty(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	empty, err := q.IsEmpty()
	if err != nil {
		t.Fatalf("IsEmpty() error = %v", err)
	}
	if !empty {
		t.Error("IsEmpty() = false, want true")
	}

	q.Add("Test", nil, "", 0)

	empty, err = q.IsEmpty()
	if err != nil {
		t.Fatalf("IsEmpty() error = %v", err)
	}
	if empty {
		t.Error("IsEmpty() = true, want false")
	}
}

func TestQueue_Delete(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	q.Add("Test", nil, "", 0)

	// Verify queue file exists
	queuePath := filepath.Join(dir, queueFileName)
	if _, err := os.Stat(queuePath); os.IsNotExist(err) {
		t.Fatal("Queue file should exist after Add()")
	}

	// Delete the queue
	if err := q.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify queue file is gone
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Error("Queue file should not exist after Delete()")
	}

	// Delete again should not error
	if err := q.Delete(); err != nil {
		t.Errorf("Delete() on non-existent queue error = %v", err)
	}
}

func TestQueue_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	const numGoroutines = 10
	const messagesPerGoroutine = 5

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Concurrent adds (0 = no limit)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				_, err := q.Add("message", nil, "", 0)
				if err != nil {
					t.Errorf("Concurrent Add() error = %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all messages were added
	length, err := q.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	expected := numGoroutines * messagesPerGoroutine
	if length != expected {
		t.Errorf("Len() = %d, want %d", length, expected)
	}
}

func TestQueue_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create queue and add messages
	q1 := NewQueue(dir)
	msg1, _ := q1.Add("First", nil, "client1", 0)
	msg2, _ := q1.Add("Second", []string{"img1"}, "client2", 0)

	// Create new queue instance pointing to same directory
	q2 := NewQueue(dir)

	// Should see the same messages
	messages, err := q2.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("Persisted message[0].ID = %q, want %q", messages[0].ID, msg1.ID)
	}
	if messages[1].ID != msg2.ID {
		t.Errorf("Persisted message[1].ID = %q, want %q", messages[1].ID, msg2.ID)
	}
	if messages[1].ImageIDs[0] != "img1" {
		t.Errorf("Persisted message[1].ImageIDs = %v, want [img1]", messages[1].ImageIDs)
	}
}

func TestQueue_MaxSize(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	const maxSize = 3

	// Add messages up to the limit
	for i := 0; i < maxSize; i++ {
		_, err := q.Add("message", nil, "", maxSize)
		if err != nil {
			t.Fatalf("Add() error = %v (message %d)", err, i+1)
		}
	}

	// Verify queue is at capacity
	length, _ := q.Len()
	if length != maxSize {
		t.Errorf("Len() = %d, want %d", length, maxSize)
	}

	// Try to add one more - should fail with ErrQueueFull
	_, err := q.Add("overflow", nil, "", maxSize)
	if err != ErrQueueFull {
		t.Errorf("Add() when full error = %v, want ErrQueueFull", err)
	}

	// Queue length should still be at max
	length, _ = q.Len()
	if length != maxSize {
		t.Errorf("Len() after failed add = %d, want %d", length, maxSize)
	}

	// Remove one message
	q.Pop()

	// Now we should be able to add again
	_, err = q.Add("new message", nil, "", maxSize)
	if err != nil {
		t.Errorf("Add() after Pop() error = %v", err)
	}

	// Queue should be at capacity again
	length, _ = q.Len()
	if length != maxSize {
		t.Errorf("Len() after re-add = %d, want %d", length, maxSize)
	}
}

func TestQueue_MaxSize_Zero_NoLimit(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// With maxSize=0, there should be no limit
	for i := 0; i < 100; i++ {
		_, err := q.Add("message", nil, "", 0)
		if err != nil {
			t.Fatalf("Add() with no limit error = %v (message %d)", err, i+1)
		}
	}

	length, _ := q.Len()
	if length != 100 {
		t.Errorf("Len() = %d, want 100", length)
	}
}

func TestQueue_MaxSize_Negative_NoLimit(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// With maxSize<0, there should be no limit
	for i := 0; i < 10; i++ {
		_, err := q.Add("message", nil, "", -1)
		if err != nil {
			t.Fatalf("Add() with negative limit error = %v (message %d)", err, i+1)
		}
	}

	length, _ := q.Len()
	if length != 10 {
		t.Errorf("Len() = %d, want 10", length)
	}
}
