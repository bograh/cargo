package api

import (
	"context"
	"net/http"

	"github.com/bograh/cargo/internal/db/sqlc"
)

type ctxKey int

const userKey ctxKey = 0

func userFrom(ctx context.Context) sqlc.User {
	u, _ := ctx.Value(userKey).(sqlc.User)
	return u
}

// requireAuth resolves the access cookie to a user or rejects with 401.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("cargo_access")
		if err != nil {
			Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		u, err := s.auth.UserForAccessToken(r.Context(), c.Value)
		if err != nil {
			Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

// requireInstanceAdmin must be nested inside requireAuth.
func (s *Server) requireInstanceAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !userFrom(r.Context()).IsInstanceAdmin {
			Error(w, http.StatusForbidden, "forbidden", "instance admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
