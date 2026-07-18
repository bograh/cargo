package api

import (
	"github.com/bograh/cargo/internal/webui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(s *Server) *chi.Mux {
	r := chi.NewMux()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/healthz", HealthHandler)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/instance/info", s.getInstanceInfo)

		r.Route("/auth", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(authRateLimiter())
				r.Post("/register", s.handleRegister)
				r.Post("/login", s.handleLogin)
				r.Post("/refresh", s.handleRefresh)
			})
			r.Post("/logout", s.handleLogout)
			r.With(s.requireAuth).Get("/me", s.handleMe)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Route("/orgs", func(r chi.Router) {
				r.Post("/", s.handleCreateOrg)
				r.Get("/", s.handleListOrgs)
				r.Route("/{orgID}", func(r chi.Router) {
					r.Get("/", s.handleGetOrg)
					r.Delete("/", s.handleDeleteOrg)
					r.Get("/members", s.handleListMembers)
					r.Patch("/members/{userID}", s.handleUpdateMemberRole)
					r.Delete("/members/{userID}", s.handleRemoveMember)
					r.Post("/invites", s.handleCreateInvite)
					r.Get("/invites", s.handleListInvites)
					r.Delete("/invites/{inviteID}", s.handleRevokeInvite)
					r.Post("/apps", s.handleCreateApp)
					r.Get("/apps", s.handleListApps)
					r.Get("/github", s.handleOrgGithubStatus)
					r.Get("/github/repos", s.handleGithubRepos)
					r.Get("/github/repos/{owner}/{repo}/branches", s.handleGithubBranches)
				})
			})
			r.Route("/apps/{appID}", func(r chi.Router) {
				r.Get("/", s.handleGetApp)
				r.Patch("/", s.handleUpdateApp)
				r.Delete("/", s.handleDeleteApp)
				r.Get("/env", s.handleListEnvKeys)
				r.Put("/env", s.handleSetEnvVars)
				r.Delete("/env/{key}", s.handleDeleteEnvVar)
				r.Post("/deploy", s.handleDeploy)
				r.Post("/rollback", s.handleRollback)
				r.Get("/deployments", s.handleListDeployments)
			})
			r.Route("/deployments/{deploymentID}", func(r chi.Router) {
				r.Get("/", s.handleGetDeployment)
				r.Get("/logs", s.handleDeploymentLogs)
			})
			r.Post("/invites/accept", s.handleAcceptInvite)
			r.Get("/github/setup", s.handleGithubSetup)
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requireAuth, s.requireInstanceAdmin)
			r.Get("/users", s.handleAdminListUsers)
			r.Get("/orgs", s.handleAdminListOrgs)
			r.Get("/settings/github-app", s.handleGetGithubApp)
			r.Put("/settings/github-app", s.handlePutGithubApp)
		})

		r.Post("/webhooks/github", s.handleGithubWebhook)
	})
	r.Mount("/", webui.Handler())
	return r
}
