package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/hostmgr"
	"github.com/bograh/cargo/internal/hosts"
	"github.com/go-chi/chi/v5"
)

// HostsAdmin is satisfied by *hostmgr.Manager.
type HostsAdmin interface {
	Create(ctx context.Context, in hosts.CreateInput) (sqlc.Host, error)
	List(ctx context.Context) ([]sqlc.Host, error)
	Delete(ctx context.Context, id string) error
	ReplaceKey(ctx context.Context, id string, keyPEM string) error
	Verify(ctx context.Context, h sqlc.Host) (hostmgr.VerifyResult, error)
}

// HostReader is satisfied by *hosts.Service.
type HostReader interface {
	GetByID(ctx context.Context, id string) (sqlc.Host, error)
}

// HostJSON is the wire shape of a worker host. The sealed key material is
// write-only: it never appears in any response.
type HostJSON struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Address          string `json:"address"`
	Port             int32  `json:"port"`
	Status           string `json:"status"`
	EngineVersion    string `json:"engine_version,omitempty"`
	CPUCount         int32  `json:"cpu_count,omitempty"`
	MemTotalMB       int64  `json:"mem_total_mb,omitempty"`
	AppsDomainSuffix string `json:"apps_domain_suffix,omitempty"`
	LetsEncryptEmail string `json:"letsencrypt_email,omitempty"`
	HasKey           bool   `json:"has_key"`
	Fingerprint      string `json:"host_key_fingerprint,omitempty"`
}

func hostJSON(h sqlc.Host) HostJSON {
	return HostJSON{
		ID:               uuidString(h.ID),
		Name:             h.Name,
		Address:          h.Address,
		Port:             h.Port,
		Status:           h.Status,
		EngineVersion:    h.EngineVersion.String,
		CPUCount:         h.CpuCount.Int32,
		MemTotalMB:       h.MemTotalMb.Int64,
		AppsDomainSuffix: h.AppsDomainSuffix,
		LetsEncryptEmail: h.LetsencryptEmail,
		HasKey:           len(h.PrivateKeyEnc) > 0,
		Fingerprint:      h.HostKeyFingerprint.String,
	}
}

// handleCreateHost registers a worker host and verifies it synchronously so
// the admin immediately sees online/degraded/unreachable plus any install
// hint.
func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	if s.hostsAdmin == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "worker hosts are not available")
		return
	}
	var body struct {
		Name             string `json:"name"`
		Address          string `json:"address"`
		Port             int    `json:"port"`
		KeyPEM           string `json:"key_pem"`
		AppsDomainSuffix string `json:"apps_domain_suffix"`
		LetsEncryptEmail string `json:"letsencrypt_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Address == "" || body.KeyPEM == "" {
		Error(w, http.StatusBadRequest, "validation_failed",
			"name, address and key_pem are required")
		return
	}
	h, err := s.hostsAdmin.Create(r.Context(), hosts.CreateInput{
		Name:             body.Name,
		Address:          body.Address,
		Port:             body.Port,
		KeyPEM:           []byte(body.KeyPEM),
		DomainSuffix:     body.AppsDomainSuffix,
		LetsEncryptEmail: body.LetsEncryptEmail,
	})
	if errors.Is(err, hosts.ErrValidation) {
		Error(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not create host")
		return
	}
	res := map[string]any{"host": hostJSON(h)}
	if vr, verr := s.hostsAdmin.Verify(r.Context(), h); verr == nil {
		res["verify"] = map[string]any{
			"status":         vr.Status,
			"engine_version": vr.EngineVersion,
			"cpu_count":      vr.CPUCount,
			"mem_total_mb":   vr.MemTotalMB,
			"install_hint":   vr.InstallHint,
		}
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	if s.hostsAdmin == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "worker hosts are not available")
		return
	}
	list, err := s.hostsAdmin.List(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not list hosts")
		return
	}
	out := make([]HostJSON, 0, len(list))
	for _, h := range list {
		out = append(out, hostJSON(h))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	if s.hostsAdmin == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "worker hosts are not available")
		return
	}
	id := chi.URLParam(r, "hostID")
	err := s.hostsAdmin.Delete(r.Context(), id)
	if errors.Is(err, hosts.ErrHostInUse) {
		Error(w, http.StatusConflict, "host_in_use", err.Error())
		return
	}
	if errors.Is(err, hosts.ErrNotFound) {
		Error(w, http.StatusNotFound, "not_found", "host not found")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not delete host")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleReplaceHostKey re-seals a new private key for the host and
// re-verifies — the recovery path when the stored key is unreadable or was
// rotated on the worker.
func (s *Server) handleReplaceHostKey(w http.ResponseWriter, r *http.Request) {
	if s.hostsAdmin == nil {
		Error(w, http.StatusServiceUnavailable, "unavailable", "worker hosts are not available")
		return
	}
	id := chi.URLParam(r, "hostID")
	var body struct {
		KeyPEM string `json:"key_pem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.KeyPEM == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "key_pem is required")
		return
	}
	if err := s.hostsAdmin.ReplaceKey(r.Context(), id, body.KeyPEM); err != nil {
		if errors.Is(err, hosts.ErrValidation) {
			Error(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		Error(w, http.StatusInternalServerError, "internal", "could not replace key")
		return
	}
	h, err := s.hostSvc.GetByID(r.Context(), id)
	if err == nil {
		_, _ = s.hostsAdmin.Verify(r.Context(), h)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "replaced"})
}

// handleListOrgHosts is the org-scoped read-only list feeding the app form's
// host selector. Any org member may read it; only instance admins manage.
func (s *Server) handleListOrgHosts(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	if _, _, err := s.orgs.Get(r.Context(), orgID, userFrom(r.Context()).ID); err != nil {
		Error(w, http.StatusNotFound, "not_found", "organization not found")
		return
	}
	if s.hostsAdmin == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	list, err := s.hostsAdmin.List(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "could not list hosts")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, h := range list {
		out = append(out, map[string]any{
			"id":      uuidString(h.ID),
			"name":    h.Name,
			"status":  h.Status,
			"address": h.Address,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
