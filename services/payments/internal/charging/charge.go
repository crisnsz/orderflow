package charging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

func (s *Service) charge(ctx context.Context, idempotencyKey, cardToken string, amountCents int64) (authorizationRef, saleRef string, err error) {
	authorizationRef, err = s.bank.Authorize(ctx, idempotencyKey, cardToken, amountCents)
	if err != nil {
		return "", "", fmt.Errorf("authorize: %w", err)
	}
	saleRef, err = s.bank.Sale(ctx, idempotencyKey, authorizationRef, amountCents)
	if err != nil {
		return authorizationRef, "", s.releaseAuthorization(ctx, idempotencyKey, authorizationRef, err)
	}
	return authorizationRef, saleRef, nil
}

func (s *Service) releaseAuthorization(ctx context.Context, idempotencyKey, authorizationRef string, saleErr error) error {
	voidErr := s.bank.Void(ctx, idempotencyKey, authorizationRef)
	if voidErr == nil {
		slog.WarnContext(ctx, "sale failed; authorization voided", "auth_ref", authorizationRef)
		return fmt.Errorf("sale: %w", saleErr)
	}
	if errors.Is(saleErr, ErrDeclined) {
		slog.ErrorContext(ctx, "sale declined and void failed; authorization still held, void it manually",
			"auth_ref", authorizationRef, "error", voidErr)
		return fmt.Errorf("sale: %w", saleErr)
	}
	return fmt.Errorf("sale failed (%s) and void failed: %w", saleErr.Error(), voidErr)
}
