package server

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"vonet/internal/device"
)

const (
	ussiSessionPrefix = "ussi-"
	ussdSessionTTL    = 15 * time.Minute
)

// ussdSessionStore is the server-layer counterpart of device.Manager's USSD
// session map. Each USSI token binds a configured device ID to the exact SIP
// dialog ID; the IMS session owns the network dialog itself. The prefix keeps
// expired USSI tokens from ever being interpreted as cellular session tokens.
type ussdSessionStore struct {
	mu       sync.Mutex
	sessions map[string]ussdServerSession
}

type ussdServerSession struct {
	deviceID         string
	networkSessionID string
	createdAt        time.Time
}

func (session ussdServerSession) expired(now time.Time) bool {
	return !session.createdAt.IsZero() && !now.Before(session.createdAt.Add(ussdSessionTTL))
}

func (store *ussdSessionStore) pruneExpiredLocked(now time.Time) {
	for id, session := range store.sessions {
		if session.expired(now) {
			delete(store.sessions, id)
		}
	}
}

func newUSSDSessionStore() ussdSessionStore {
	return ussdSessionStore{sessions: make(map[string]ussdServerSession)}
}

func (store *ussdSessionStore) openWithNetwork(deviceID, networkSessionID string) string {
	var token [8]byte
	_, _ = rand.Read(token[:])
	id := ussiSessionPrefix + hex.EncodeToString(token[:])
	now := time.Now().UTC()
	store.mu.Lock()
	if store.sessions == nil {
		store.sessions = make(map[string]ussdServerSession)
	}
	store.pruneExpiredLocked(now)
	store.sessions[id] = ussdServerSession{
		deviceID:         deviceID,
		networkSessionID: networkSessionID,
		createdAt:        now,
	}
	store.mu.Unlock()
	return id
}

func (store *ussdSessionStore) networkSession(sessionID string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	store.pruneExpiredLocked(now)
	session, ok := store.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return ""
	}
	return session.networkSessionID
}

func (store *ussdSessionStore) device(sessionID string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	store.pruneExpiredLocked(now)
	session, ok := store.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return "", device.ErrUSSDSessionNotFound
	}
	return session.deviceID, nil
}

func (store *ussdSessionStore) drop(sessionID string) {
	store.mu.Lock()
	delete(store.sessions, strings.TrimSpace(sessionID))
	store.mu.Unlock()
}
