package bank

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/crisnsz/orderflow/services/payments/internal/charging"
)

const (
	StageNone = ""
	StageAuth = "auth"
	StageSale = "sale"
)

type Switch struct {
	mu    sync.RWMutex
	stage string
}

func NewSwitch(stage string) *Switch { return &Switch{stage: normalizeStage(stage)} }

func normalizeStage(stage string) string {
	switch strings.ToLower(stage) {
	case StageAuth:
		return StageAuth
	case StageSale:
		return StageSale
	}
	return StageNone
}

func (s *Switch) Stage() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.stage }

func (s *Switch) Set(stage string) { s.mu.Lock(); s.stage = normalizeStage(stage); s.mu.Unlock() }

type Fake struct{ failureSwitch *Switch }

var _ charging.Bank = (*Fake)(nil)

func NewFake(failureSwitch *Switch) *Fake { return &Fake{failureSwitch: failureSwitch} }

const maxReferenceKeyLength = 12

func bankReference(prefix, idempotencyKey string) string {
	shortKey := strings.ReplaceAll(idempotencyKey, "-", "")
	if len(shortKey) > maxReferenceKeyLength {
		shortKey = shortKey[:maxReferenceKeyLength]
	}
	return prefix + "_" + shortKey
}

func (f *Fake) Authorize(ctx context.Context, idempotencyKey, _ string, amountCents int64) (string, error) {
	if f.failureSwitch.Stage() == StageAuth {
		slog.WarnContext(ctx, "fake bank: authorization declined (forced failure)", "amount_cents", amountCents)
		return "", fmt.Errorf("%w: do_not_honor (simulated failure)", charging.ErrDeclined)
	}
	slog.InfoContext(ctx, "fake bank: authorized", "amount_cents", amountCents)
	return bankReference("auth", idempotencyKey), nil
}

func (f *Fake) Sale(ctx context.Context, idempotencyKey, authorizationRef string, amountCents int64) (string, error) {
	if f.failureSwitch.Stage() == StageSale {
		slog.WarnContext(ctx, "fake bank: sale failed (forced failure)", "auth_ref", authorizationRef)
		return "", fmt.Errorf("%w: capture_failed (simulated failure)", charging.ErrDeclined)
	}
	slog.InfoContext(ctx, "fake bank: sale captured", "auth_ref", authorizationRef, "amount_cents", amountCents)
	return bankReference("sale", idempotencyKey), nil
}

func (f *Fake) Void(ctx context.Context, _ string, authorizationRef string) error {
	slog.InfoContext(ctx, "fake bank: authorization voided", "auth_ref", authorizationRef)
	return nil
}
