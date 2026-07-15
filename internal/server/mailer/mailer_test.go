package mailer

import (
	"strings"
	"testing"
)

func TestMailer_Disabled(t *testing.T) {
	m := New("", "", "", "", "", false)
	if m.Enabled() {
		t.Error("expected mailer to be disabled when host is empty")
	}

	// Should not error when disabled
	err := m.Send("to@test.com", "Subject", "Body")
	if err != nil {
		t.Errorf("expected no error when sending with disabled mailer, got %v", err)
	}
}

func TestMailer_Enabled(t *testing.T) {
	m := New("smtp.example.com", "587", "user", "pass", "from@example.com", false)
	if !m.Enabled() {
		t.Error("expected mailer to be enabled when host is provided")
	}
}

func TestMailer_Send_ConnectionError(t *testing.T) {
	// Using a non-existent port to force connection failure
	m := New("127.0.0.1", "1", "user", "pass", "from@example.com", false)
	err := m.Send("to@test.com", "Subj", "Body")
	if err == nil {
		t.Error("expected connection error, got nil")
	}
}

func TestMailer_SendTLS_ConnectionError(t *testing.T) {
	m := New("127.0.0.1", "1", "user", "pass", "from@example.com", true)
	err := m.Send("to@test.com", "Subj", "Body")
	if err == nil {
		t.Error("expected connection error, got nil")
	} else if !strings.Contains(err.Error(), "tls dial") {
		t.Errorf("expected tls dial error, got: %v", err)
	}
}

func TestMailer_SendInvite_ConnectionError(t *testing.T) {
	m := New("127.0.0.1", "1", "", "", "from@example.com", false)
	err := m.SendInvite("to@test.com", "http://invite.link")
	if err == nil {
		t.Error("expected connection error on SendInvite, got nil")
	}
}

func TestMailer_SendPasswordReset_ConnectionError(t *testing.T) {
	m := New("127.0.0.1", "1", "", "", "from@example.com", false)
	err := m.SendPasswordReset("to@test.com", "http://reset.link")
	if err == nil {
		t.Error("expected connection error on SendPasswordReset, got nil")
	}
}
