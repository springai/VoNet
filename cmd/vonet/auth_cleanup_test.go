package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"vonet/internal/store"
)

func TestAuthCleanupRevokesRestoredCredentialsAndPreservesSSOConfiguration(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/restored.db"
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(ctx, 1, []byte("session-hash"), []byte("csrf-hash"), time.Now().Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAPIToken(ctx, "restored-token", []byte("token-hash"), false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	secret := "initial-secret"
	snapshot, revoked, err := database.SaveSSOConfiguration(ctx, store.SSOSaveRequest{
		Config: store.SSOConfig{
			Kind: "pocketid", ClientID: "vonet", IssuerURL: "https://id.example.com",
			PublicOrigin: "https://vonet.example.com", Scopes: []string{"openid", "profile", "email"},
			TokenAuthMethod: "client_secret_basic", PKCEMode: "s256",
		},
		ClientSecret: &secret,
	})
	if err != nil || revoked {
		t.Fatalf("initial SSO configuration: revoked=%v, err=%v", revoked, err)
	}
	bindFlow := store.SSOFlow{
		ID:                    "auth-cleanup-bind",
		StateHash:             bytes.Repeat([]byte{1}, 32),
		BrowserTokenHash:      bytes.Repeat([]byte{2}, 32),
		Purpose:               "bind",
		ProviderInstanceID:    snapshot.Config.ProviderInstanceID,
		ConfigRevision:        snapshot.Config.Revision,
		InitiatingSessionHash: []byte("session-hash"),
		AdminID:               1,
		NonceHash:             bytes.Repeat([]byte{3}, 32),
		PKCEVerifier:          "bind-verifier",
		RedirectURI:           "https://vonet.example.com/api/auth/sso/callback",
		CreatedAt:             time.Now().UTC().Truncate(time.Second),
		ExpiresAt:             time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second),
	}
	if err := database.CreateSSOFlow(ctx, bindFlow, nil); err != nil {
		t.Fatal(err)
	}
	claimedBindFlow, err := database.ClaimSSOFlow(ctx, bindFlow.StateHash, bindFlow.BrowserTokenHash)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = database.FinalizeSSOBinding(ctx, claimedBindFlow, store.SSOIdentity{
		Protocol: "oidc", Authority: "https://id.example.com", Subject: "stable-sub",
		DisplayName: "Example user", Email: "user@example.com",
	})
	if err != nil || snapshot.Binding == nil {
		t.Fatalf("external SSO binding: snapshot=%+v, err=%v", snapshot, err)
	}
	snapshot.Config.Enabled = true
	snapshot, revoked, err = database.SaveSSOConfiguration(ctx, store.SSOSaveRequest{
		ExpectedRevision: snapshot.Config.Revision, Config: snapshot.Config,
	})
	if err != nil || revoked {
		t.Fatalf("enable bound SSO configuration: revoked=%v, err=%v", revoked, err)
	}
	pendingFlow := store.SSOFlow{
		ID:                 "auth-cleanup-login",
		StateHash:          bytes.Repeat([]byte{4}, 32),
		BrowserTokenHash:   bytes.Repeat([]byte{5}, 32),
		Purpose:            "login",
		ProviderInstanceID: snapshot.Config.ProviderInstanceID,
		ConfigRevision:     snapshot.Config.Revision,
		NonceHash:          bytes.Repeat([]byte{6}, 32),
		PKCEVerifier:       "login-verifier",
		RedirectURI:        "https://vonet.example.com/api/auth/sso/callback",
		CreatedAt:          time.Now().UTC().Truncate(time.Second),
		ExpiresAt:          time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second),
	}
	if err := database.CreateSSOFlow(ctx, pendingFlow, nil); err != nil {
		t.Fatal(err)
	}
	adminBefore, err := database.CurrentAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ssoSettingBefore, err := database.AppSetting(ctx, store.SSOConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	bindingBefore := *snapshot.Binding
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runAuthCleanup([]string{"--database", path}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "revoked") {
		t.Fatalf("missing cleanup output: %q", output.String())
	}

	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.SessionByTokenHash(ctx, []byte("session-hash")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored session survived cleanup: %v", err)
	}
	if _, err := database.APITokenByHash(ctx, []byte("token-hash")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored API token survived cleanup: %v", err)
	}
	if _, err := database.SSOFlowByStateHash(ctx, pendingFlow.StateHash); !errors.Is(err, store.ErrSSOInvalidFlow) {
		t.Fatalf("pending SSO flow survived cleanup: %v", err)
	}
	adminAfter, err := database.CurrentAdmin(ctx)
	if err != nil || adminAfter.CredentialEpoch != adminBefore.CredentialEpoch+1 {
		t.Fatalf("credential epoch = %d, err = %v, want %d", adminAfter.CredentialEpoch, err, adminBefore.CredentialEpoch+1)
	}
	setting, err := database.AppSetting(ctx, store.SSOConfigKey)
	if err != nil || !bytes.Equal(setting.Value, ssoSettingBefore.Value) {
		t.Fatalf("SSO configuration was not preserved: %+v, %v", setting, err)
	}
	snapshotAfter, err := database.SSOSnapshot(ctx)
	if err != nil || snapshotAfter.Binding == nil || *snapshotAfter.Binding != bindingBefore {
		t.Fatalf("external SSO binding was not preserved: %+v, %v", snapshotAfter.Binding, err)
	}
}
