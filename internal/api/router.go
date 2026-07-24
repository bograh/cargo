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
		// Public: lets an invited user preview an invite before signing in.
		r.Get("/invites/{token}", s.handlePreviewInvite)

		r.Route("/auth", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(authRateLimiter())
				r.Post("/register", s.handleRegister)
				r.Post("/login", s.handleLogin)
				r.Post("/refresh", s.handleRefresh)
				r.Get("/providers", s.handleAuthProviders)
				r.Get("/oidc/start", s.handleOIDCStart)
				r.Get("/oidc/callback", s.handleOIDCCallback)
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
					r.Post("/databases", s.handleCreateDatabase)
					r.Get("/databases", s.handleListDatabases)
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
				r.Get("/domains", s.handleListDomains)
				r.Post("/domains", s.handleAddDomain)
				r.Delete("/domains/{domainID}", s.handleRemoveDomain)
				r.Post("/deploy", s.handleDeploy)
				r.Post("/rollback", s.handleRollback)
				r.Post("/stop", s.handleStopApp)
				r.Post("/start", s.handleStartApp)
				r.Get("/deployments", s.handleListDeployments)
				r.Get("/logs", s.handleAppLogs)
				r.Get("/metrics", s.handleAppMetrics)
				r.Get("/metrics/stream", s.handleAppMetricsStream)
			})
			r.Route("/deployments/{deploymentID}", func(r chi.Router) {
				r.Get("/", s.handleGetDeployment)
				r.Get("/logs", s.handleDeploymentLogs)
			})
			r.Route("/databases/{dbID}", func(r chi.Router) {
				r.Get("/", s.handleGetDatabase)
				r.Delete("/", s.handleDeleteDatabase)
				r.Get("/logs", s.handleDatabaseLogs)
				r.Post("/attachments", s.handleAttachDatabase)
				r.Delete("/attachments/{appID}", s.handleDetachDatabase)
				r.Post("/snapshots", s.handleCreateSnapshot)
				r.Get("/snapshots", s.handleListSnapshots)
				r.Get("/snapshots/{name}", s.handleGetSnapshot)
				r.Delete("/snapshots/{name}", s.handleDeleteSnapshot)
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
			r.Get("/settings/github-app/manifest", s.handleGithubManifestStart)
			r.Get("/settings/github-app/manifest/callback", s.handleGithubManifestCallback)
			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings/apps-domain-suffix", s.handlePutSuffix)
			r.Put("/settings/smtp", s.handlePutSMTP)
			r.Delete("/settings/smtp", s.handleDeleteSMTP)
			r.Post("/settings/smtp/test", s.handleTestSMTP)
			r.Get("/settings/oidc", s.handleGetOIDC)
			r.Put("/settings/oidc", s.handlePutOIDC)
			r.Delete("/settings/oidc", s.handleDeleteOIDC)
			r.Post("/backups", s.handleRunBackup)
			r.Get("/backups", s.handleListBackups)
			r.Get("/disk", s.handleDiskStatus)
		})

		r.Post("/webhooks/github", s.handleGithubWebhook)
	})
	r.Mount("/", webui.Handler())
	return r
}
