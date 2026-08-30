package httpsender

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSend_HeadersAndStatus проверяет, что Sender передаёт X-Signature,
// кастомный User-Agent и payload, и возвращает HTTP-статус сервера.
func TestSend_HeadersAndStatus(t *testing.T) {
	var gotSig, gotUA, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Signature")
		gotUA = r.Header.Get("User-Agent")
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New()
	ctx := context.Background()
	code, err := s.Send(ctx, srv.URL, "webhook-dispatcher/1.0", "sig-abc", []byte(`{"event":"order"}`))
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want %d", code, http.StatusOK)
	}
	if gotSig != "sig-abc" {
		t.Errorf("X-Signature = %q, want %q", gotSig, "sig-abc")
	}
	if gotUA != "webhook-dispatcher/1.0" {
		t.Errorf("User-Agent = %q, want %q", gotUA, "webhook-dispatcher/1.0")
	}
	if gotBody != `{"event":"order"}` {
		t.Errorf("body = %q, want event payload", gotBody)
	}
}

// TestSend_NetworkError возвращает код 0 и ошибку при недоступном адресе.
func TestSend_NetworkError(t *testing.T) {
	s := New()
	// Адрес на неиспользуемом зарезервированном порту — соединение упадёт быстро.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	code, err := s.Send(ctx, "http://127.0.0.1:1/", "ua", "sig", []byte("{}"))
	if err == nil {
		t.Fatalf("Send: want error, got nil (code=%d)", code)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 on network error", code)
	}
}

// TestSend_ContextCancel уважает отмену контекста через таймаут клиента.
func TestSend_ContextCancel(t *testing.T) {
	// Сервер засыпает дольше таймаута клиента (5s), клиент должен вернуться с ошибкой.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	ctx := context.Background()
	// Ранний отменяющий контекст, чтобы тест не висел.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()

	s := New()
	code, err := s.Send(ctx, srv.URL, "ua", "sig", []byte("{}"))
	if err == nil {
		t.Fatalf("Send: want error on cancelled ctx, got nil (code=%d)", code)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}
