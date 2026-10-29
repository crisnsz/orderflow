package charging

import (
	"context"
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
		if voidErr := s.bank.Void(ctx, idempotencyKey, authorizationRef); voidErr != nil {
			return authorizationRef, "", fmt.Errorf("sale failed (%s) and void failed: %w", err.Error(), voidErr)
		}
		slog.WarnContext(ctx, "sale failed; authorization voided", "auth_ref", authorizationRef)
		return authorizationRef, "", fmt.Errorf("sale: %w", err)
	}
	return authorizationRef, saleRef, nil
}
