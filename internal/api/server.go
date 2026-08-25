package api

import (
	"encoding/json"
	"net/http"
)

type Server struct {
	version string
}

func NewServer(version string) *Server {
	return &Server{version: version}
}

func (s *Server) GetHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{
		Status:  Ok,
		Version: s.version,
	})
}
