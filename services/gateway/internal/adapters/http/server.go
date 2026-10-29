package http

import (
	"embed"
	"io/fs"
	nethttp "net/http"
	"time"

	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
	"github.com/crisnsz/orderflow/services/gateway/internal/storefront"
	"github.com/crisnsz/orderflow/services/gateway/internal/timeline"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

//go:embed static
var staticFS embed.FS

type Deps struct {
	Signer       *domain.Signer
	Orders       storefront.Orders
	Catalog      storefront.Catalog
	Payments     storefront.Payments
	Hub          *timeline.Hub
	SecureCookie bool
	SessionTTL   time.Duration
}

type Server struct{ Deps }

func NewHandler(deps Deps) nethttp.Handler {
	s := &Server{deps}
	mux := nethttp.NewServeMux()
	staticFiles, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /", nethttp.FileServerFS(staticFiles))

	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /api/me", s.authenticated(s.me))
	mux.HandleFunc("GET /api/products", s.authenticated(s.products))
	mux.HandleFunc("POST /api/orders", s.authenticated(s.placeOrder))
	mux.HandleFunc("GET /api/orders", s.authenticated(s.listOrders))
	mux.HandleFunc("GET /api/orders/{id}", s.authenticated(s.getOrder))
	mux.HandleFunc("GET /api/orders/{id}/events", s.authenticated(s.streamEvents))
	mux.HandleFunc("GET /api/admin/failure", s.authenticated(s.getFailure))
	mux.HandleFunc("PUT /api/admin/failure", s.authenticated(s.setFailure))

	return otelhttp.NewHandler(securityHeaders(mux), "gateway",
		otelhttp.WithFilter(func(r *nethttp.Request) bool { return r.URL.Path != "/healthz" }))
}

func securityHeaders(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
