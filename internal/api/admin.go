package api

import "net/http"

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.admin.ListUsers(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "listing users failed")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, userJSON(u)) // userJSON never includes the password hash
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminListOrgs(w http.ResponseWriter, r *http.Request) {
	list, err := s.admin.ListAllOrganizations(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "listing organizations failed")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, o := range list {
		out = append(out, orgJSON(o))
	}
	writeJSON(w, http.StatusOK, out)
}
