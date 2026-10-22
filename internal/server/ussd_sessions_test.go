package server

import (
	"errors"
	"testing"
	"time"

	"vonet/internal/device"
)

func TestUSSDServerSessionsExpireAndArePruned(t *testing.T) {
	sessions := newUSSDSessionStore()
	token := sessions.openWithNetwork("saved-device", "dialog-1")

	sessions.mu.Lock()
	session := sessions.sessions[token]
	session.createdAt = time.Now().UTC().Add(-ussdSessionTTL - time.Second)
	sessions.sessions[token] = session
	sessions.mu.Unlock()

	if _, err := sessions.device(token); !errors.Is(err, device.ErrUSSDSessionNotFound) {
		t.Fatalf("expired session device lookup = %v", err)
	}
	if sessions.networkSession(token) != "" {
		t.Fatal("expired session retained its SIP dialog binding")
	}

	sessions.mu.Lock()
	sessions.sessions[token] = session
	sessions.mu.Unlock()
	sessions.openWithNetwork("saved-device", "dialog-2")
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	if len(sessions.sessions) != 1 {
		t.Fatalf("expired sessions not pruned: %#v", sessions.sessions)
	}
}
