package http

import (
	"context"
	"encoding/json"
	"log/slog"
	nethttp "net/http"

	"github.com/crisnsz/orderflow/services/payments/internal/adapters/bank"
	"github.com/crisnsz/orderflow/services/payments/internal/domain"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type CardReader interface {
	CardFor(ctx context.Context, userID string) (*domain.Card, error)
}

type cardResponse struct {
	Brand          string `json:"brand"`
	Last4          string `json:"last4"`
	ExpMonth       int    `json:"exp_month"`
	ExpYear        int    `json:"exp_year"`
	AutopayConsent bool   `json:"autopay_consent"`
}

type failureStageBody struct {
	Stage string `json:"stage"`
}

const maxFailureBodyBytes = 1 << 10

func Handler(cards CardReader, failureSwitch *bank.Switch) nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /cards", listCards(cards))
	mux.HandleFunc("GET /admin/failure", getFailureStage(failureSwitch))
	mux.HandleFunc("PUT /admin/failure", setFailureStage(failureSwitch))
	return otelhttp.NewHandler(mux, "payments-admin")
}

func listCards(cards CardReader) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		card, err := cards.CardFor(r.Context(), r.URL.Query().Get("user_id"))
		if err != nil {
			slog.ErrorContext(r.Context(), "card lookup failed", "error", err)
			nethttp.Error(w, "internal error", nethttp.StatusInternalServerError)
			return
		}
		response := []cardResponse{}
		if card != nil {
			response = append(response, cardResponse{card.Brand, card.Last4, card.ExpiryMonth, card.ExpiryYear, card.AutopayConsent})
		}
		writeJSON(w, response)
	}
}

func getFailureStage(failureSwitch *bank.Switch) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		writeJSON(w, failureStageBody{Stage: failureSwitch.Stage()})
	}
}

func setFailureStage(failureSwitch *bank.Switch) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var request failureStageBody
		if err := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, maxFailureBodyBytes)).Decode(&request); err != nil {
			nethttp.Error(w, "invalid body", nethttp.StatusBadRequest)
			return
		}
		if request.Stage != bank.StageNone && request.Stage != bank.StageAuth && request.Stage != bank.StageSale {
			nethttp.Error(w, `stage must be "", "auth" or "sale"`, nethttp.StatusBadRequest)
			return
		}
		failureSwitch.Set(request.Stage)
		slog.WarnContext(r.Context(), "payment failure switch changed", "stage", failureSwitch.Stage())
		writeJSON(w, failureStageBody{Stage: failureSwitch.Stage()})
	}
}

func writeJSON(w nethttp.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
