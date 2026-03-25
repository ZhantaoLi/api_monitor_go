package dashboard

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEBus_PublishAndServeHTTP(t *testing.T) {
	bus := NewSSEBus()
	defer bus.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		bus.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("sse handler exited too early")
	case <-time.After(50 * time.Millisecond):
	}

	bus.Publish("hello", `{"x":1}`)
	time.Sleep(20 * time.Millisecond)
	bus.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("sse handler did not exit after close")
	}

	body := rr.Body.String()
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("missing initial connected event: %s", body)
	}
	if !strings.Contains(body, "event: hello") || !strings.Contains(body, `data: {"x":1}`) {
		t.Fatalf("missing published event: %s", body)
	}
}

func TestSSEBus_CloseClosesSubscribers(t *testing.T) {
	bus := NewSSEBus()
	ch := bus.subscribe()
	bus.Close()

	if _, ok := <-ch; ok {
		t.Fatalf("subscriber channel should be closed")
	}
}

func TestSSEBus_StreamFormat(t *testing.T) {
	bus := NewSSEBus()
	defer bus.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()

	go func() {
		time.Sleep(10 * time.Millisecond)
		bus.Close()
	}()
	bus.ServeHTTP(rr, req)

	scanner := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	found := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: connected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("connected event not formatted correctly: %s", rr.Body.String())
	}
}
