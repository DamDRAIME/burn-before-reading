package web

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
)

// NewAPIRouter builds the /api/v1 routes.
// Kept separate from `main.go` so tests can exercise routing and auth without a database.
func NewAPIRouter(msgHandler *MessagesHandler, pHandler *PrintersHandler, adminToken string, printerAuth printerAuthenticator, logger *slog.Logger) chi.Router {
	r := chi.NewRouter()
	msgHandler.RegisterPublicRoutes(r)
	pHandler.RegisterPublicRoutes(r)
	r.Route("/admin", func(r chi.Router) {
		r.Use(RequireAdminToken(adminToken))
		msgHandler.RegisterAdminRoutes(r)
		pHandler.RegisterAdminRoutes(r)
	})
	r.Route("/printer", func(r chi.Router) {
		r.Use(RequirePrinterToken(printerAuth, logger))
		msgHandler.RegisterPrinterRoutes(r)
		pHandler.RegisterPrinterRoutes(r)
	})
	return r
}
