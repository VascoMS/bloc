package api

import (
	"encoding/json"
	"net/http"

	"mempool-il/internal/inclusion"
	"mempool-il/internal/mempool"
)

type Server struct {
	store   *mempool.Store
	builder *inclusion.Builder
	mux     *http.ServeMux
}

func NewServer(store *mempool.Store, builder *inclusion.Builder) *Server {
	s := &Server{
		store:   store,
		builder: builder,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("/inclusion-list", s.handleInclusionList)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) handleInclusionList(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.store.Snapshot()
	list := s.builder.Build(snapshot)
	writeJSON(w, http.StatusOK, list)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
