package httpsender_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/adapter/driven/httpsender"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

func request(url string) ports.SendRequest {
	return ports.SendRequest{
		URL:       url,
		Body:      []byte(`{"event_id":"x"}`),
		Signature: "sha256=deadbeef",
		EventID:   uuid.New(),
		EventType: "order.created",
	}
}

func TestSendSetsMethodHeadersAndBody(t *testing.T) {
	var (
		gotMethod, gotUA, gotSig, gotType string
		gotBody                           []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotUA = r.Header.Get("User-Agent")
		gotSig = r.Header.Get(httpsender.SignatureHeader)
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := httpsender.New("webhookdispatcher/test", time.Second)
	req := request(srv.URL)
	result, err := sender.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.StatusCode != http.StatusOK || result.TimedOut || result.TransportError != "" {
		t.Fatalf("result = %+v, want a clean 200", result)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotUA != "webhookdispatcher/test" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if gotSig != req.Signature {
		t.Fatalf("X-Signature = %q, want %q", gotSig, req.Signature)
	}
	if gotType != "application/json" {
		t.Fatalf("Content-Type = %q", gotType)
	}
	if string(gotBody) != string(req.Body) {
		t.Fatalf("body = %q, want %q", gotBody, req.Body)
	}
}

func TestSendReportsStatusCodes(t *testing.T) {
	for _, code := range []int{200, 202, 400, 429, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		result, err := httpsender.New("ua", time.Second).Send(context.Background(), request(srv.URL))
		srv.Close()
		if err != nil {
			t.Fatalf("code %d Send: %v", code, err)
		}
		if result.StatusCode != code {
			t.Fatalf("StatusCode = %d, want %d", result.StatusCode, code)
		}
	}
}

func TestSendTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	result, err := httpsender.New("ua", 50*time.Millisecond).Send(context.Background(), request(srv.URL))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("result = %+v, want TimedOut", result)
	}
}

func TestSendReportsTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening any more

	result, err := httpsender.New("ua", time.Second).Send(context.Background(), request(url))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.TransportError == "" {
		t.Fatalf("result = %+v, want a transport error", result)
	}
}

func TestSendReturnsErrorWhenCallerIsCanceled(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	defer cancel()

	_, err := httpsender.New("ua", 5*time.Second).Send(ctx, request(srv.URL))
	if err == nil {
		t.Fatal("expected an error for a canceled caller")
	}
}

func TestSendRejectsUnusableURL(t *testing.T) {
	if _, err := httpsender.New("ua", time.Second).Send(context.Background(), request("://bad")); err == nil {
		t.Fatal("expected an error for an unusable url")
	}
}
