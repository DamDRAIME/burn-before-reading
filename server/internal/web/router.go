package web

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
)

// NewAPIRouter builds the /api/v1 routes.
// Kept separate from `main.go` so tests can exercise routing and auth without a database.
func NewAPIRouter(mHandler *MessagesHandler, pHandler *PrintersHandler, uHandler *UsersHandler, adminToken string, pAuth printerAuthenticator, uAuth userAuthenticator, logger *slog.Logger) chi.Router {
	r := chi.NewRouter()
	uHandler.RegisterPublicRoutes(r)
	r.Route("/admin", func(r chi.Router) {
		r.Use(RequireAdminToken(adminToken))
		mHandler.RegisterAdminRoutes(r)
		pHandler.RegisterAdminRoutes(r)
		uHandler.RegisterAdminRoutes(r)
	})
	r.Route("/printer", func(r chi.Router) {
		r.Use(RequirePrinterToken(pAuth, logger))
		mHandler.RegisterPrinterRoutes(r)
		pHandler.RegisterPrinterRoutes(r)
	})
	r.Route("/user", func(r chi.Router) {
		r.Use(RequireUserToken(uAuth, logger))
		mHandler.RegisterUserRoutes(r)
		pHandler.RegisterUserRoutes(r)
		uHandler.RegisterUserRoutes(r)
	})
	return r
}
