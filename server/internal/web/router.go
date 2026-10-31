package web

import "github.com/go-chi/chi/v5"

// NewAPIRouter builds the /api/v1 routes.
// Kept separate from `main.go` so tests can exercise routing and auth without a database.
func NewAPIRouter(msgHandler *MessagesHandler, adminToken string) chi.Router {
	r := chi.NewRouter()
	msgHandler.RegisterPublicRoutes(r)
	r.Route("/admin", func(r chi.Router) {
		r.Use(RequireAdminToken(adminToken))
		msgHandler.RegisterAdminRoutes(r)
	})
	return r
}
