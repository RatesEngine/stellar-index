package notify_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/notify"
)

func TestValidate_HappyPath(t *testing.T) {
	n := &notify.NoopSender{}
	err := n.Send(context.Background(), notify.Message{
		From:    "Rates Engine <hello@ratesengine.net>",
		To:      []string{"alice@example.com"},
		Subject: "Test",
		Text:    "hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n.SentCount() != 1 {
		t.Errorf("SentCount = %d, want 1", n.SentCount())
	}
}

func TestValidate_RejectsEmptyMessage(t *testing.T) {
	n := &notify.NoopSender{}
	cases := []struct {
		name string
		msg  notify.Message
	}{
		{"empty To", notify.Message{From: "x@y.com", Subject: "s", Text: "t"}},
		{"empty Subject", notify.Message{From: "x@y.com", To: []string{"a@b.com"}, Text: "t"}},
		{"empty body", notify.Message{From: "x@y.com", To: []string{"a@b.com"}, Subject: "s"}},
		{"empty From", notify.Message{To: []string{"a@b.com"}, Subject: "s", Text: "t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := n.Send(context.Background(), tc.msg)
			if !errors.Is(err, notify.ErrInvalidMessage) {
				t.Errorf("expected ErrInvalidMessage, got %v", err)
			}
		})
	}
}

func TestMagicLinkMessage_Renders(t *testing.T) {
	msg, err := notify.MagicLinkMessage(
		"Rates Engine <hello@ratesengine.net>",
		"alice@example.com",
		notify.MagicLinkInput{
			LinkURL:          "https://app.ratesengine.net/auth/callback?token=abc",
			Code:             "123456",
			ExpiresInMinutes: 15,
			IPAddress:        "203.0.113.1",
			UserAgent:        "Firefox/123",
		},
	)
	if err != nil {
		t.Fatalf("MagicLinkMessage: %v", err)
	}
	if msg.Subject == "" || msg.HTML == "" || msg.Text == "" {
		t.Fatalf("required fields not populated: %+v", msg)
	}
	// Spot-check that the dynamic fields landed in the rendered
	// bodies — guards against a refactor that drops a field
	// from the template.
	for _, want := range []string{"https://app.ratesengine.net/auth/callback?token=abc", "123456", "15 minutes", "203.0.113.1"} {
		if !strings.Contains(msg.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
		if !strings.Contains(msg.Text, want) {
			t.Errorf("Text missing %q", want)
		}
	}
	if tag, ok := msg.Tags["template"]; !ok || tag != "magic-link" {
		t.Errorf("tag template not set: %v", msg.Tags)
	}
}

func TestResendSender_HappyPath(t *testing.T) {
	var receivedAuth, receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"em_123"}`))
	}))
	defer srv.Close()

	s, err := notify.NewResendSender("re_test_token")
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	s.BaseURL = srv.URL

	err = s.Send(context.Background(), notify.Message{
		From:    "Rates Engine <hello@ratesengine.net>",
		To:      []string{"alice@example.com"},
		Subject: "Test",
		Text:    "hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receivedAuth != "Bearer re_test_token" {
		t.Errorf("Authorization header = %q", receivedAuth)
	}
	if !strings.Contains(receivedBody, `"to":["alice@example.com"]`) {
		t.Errorf("body missing To: %s", receivedBody)
	}
}

func TestResendSender_4xxIsProviderRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"unverified domain","statusCode":422}`))
	}))
	defer srv.Close()

	s, _ := notify.NewResendSender("re_test")
	s.BaseURL = srv.URL

	err := s.Send(context.Background(), notify.Message{
		From: "x@y.com", To: []string{"a@b.com"}, Subject: "s", Text: "t",
	})
	if !errors.Is(err, notify.ErrProviderRejected) {
		t.Errorf("expected ErrProviderRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "unverified domain") {
		t.Errorf("err missing detail: %v", err)
	}
}

func TestResendSender_5xxIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s, _ := notify.NewResendSender("re_test")
	s.BaseURL = srv.URL

	err := s.Send(context.Background(), notify.Message{
		From: "x@y.com", To: []string{"a@b.com"}, Subject: "s", Text: "t",
	})
	if !errors.Is(err, notify.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

func TestResendSender_RequiresAPIKey(t *testing.T) {
	if _, err := notify.NewResendSender(""); err == nil {
		t.Error("expected error for empty API key")
	}
}
