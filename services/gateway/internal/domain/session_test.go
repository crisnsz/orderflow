package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
)

func newSigner(t *testing.T, key string, now *time.Time) *domain.Signer {
	t.Helper()
	signer, err := domain.NewSigner([]byte(key), time.Hour, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestSessionRoundTripAndRejections(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	signer := newSigner(t, "0123456789abcdef", &now)
	good := signer.Issue("u-alice")

	tampered := strings.Replace(good, strings.Split(good, ".")[0], "dS1ib2I", 1)
	other := newSigner(t, "ffffffffffffffff", &now).Issue("u-alice")

	tests := []struct {
		name       string
		token      string
		at         time.Time
		want       string
		wantFailed bool
	}{
		{"valid token returns the user", good, now.Add(time.Minute), "u-alice", false},
		{"expired token is rejected", good, now.Add(2 * time.Hour), "", true},
		{"tampered payload is rejected", tampered, now, "", true},
		{"token signed with another key is rejected", other, now, "", true},
		{"garbage is rejected", "not-a-token", now, "", true},
		{"empty is rejected", "", now, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := tt.at
			userID, err := newSigner(t, "0123456789abcdef", &at).Verify(tt.token)
			if tt.wantFailed {
				if !errors.Is(err, domain.ErrInvalidSession) {
					t.Fatalf("err = %v, want ErrInvalidSession", err)
				}
				return
			}
			if err != nil || userID != tt.want {
				t.Fatalf("got %q, %v; want %q", userID, err, tt.want)
			}
		})
	}
}

func TestShortKeyIsRejected(t *testing.T) {
	if _, err := domain.NewSigner([]byte("short"), time.Hour, nil); err == nil {
		t.Fatal("want error for short key")
	}
}

func TestFindUser(t *testing.T) {
	if _, ok := domain.FindUser("u-bob"); !ok {
		t.Error("u-bob should exist")
	}
	if _, ok := domain.FindUser("u-mallory"); ok {
		t.Error("unknown user must not be found")
	}
}
