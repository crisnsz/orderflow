package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidSession = errors.New("invalid session")

type Signer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

func NewSigner(key []byte, ttl time.Duration, now func() time.Time) (*Signer, error) {
	if len(key) < minKeyLength {
		return nil, errors.New("session key must be at least 16 bytes")
	}
	if now == nil {
		now = time.Now
	}
	return &Signer{key: key, ttl: ttl, now: now}, nil
}

const (
	minKeyLength   = 16
	tokenPartCount = 3
)

func (s *Signer) signature(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Signer) Issue(userID string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(userID)) + "." + strconv.FormatInt(s.now().Add(s.ttl).Unix(), 10)
	return payload + "." + s.signature(payload)
}

func (s *Signer) Verify(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != tokenPartCount {
		return "", ErrInvalidSession
	}
	encodedUserID, expiryUnix, signature := parts[0], parts[1], parts[2]
	payload := encodedUserID + "." + expiryUnix
	if !hmac.Equal([]byte(signature), []byte(s.signature(payload))) {
		return "", ErrInvalidSession
	}
	expiry, err := strconv.ParseInt(expiryUnix, 10, 64)
	if err != nil || s.now().Unix() >= expiry {
		return "", ErrInvalidSession
	}
	userID, err := base64.RawURLEncoding.DecodeString(encodedUserID)
	if err != nil || len(userID) == 0 {
		return "", ErrInvalidSession
	}
	return string(userID), nil
}
