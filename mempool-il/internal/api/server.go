package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"mempool-il/internal/inclusion"
	"mempool-il/internal/mempool"
)

type Server struct {
	store      *mempool.Store
	builder    *inclusion.Builder
	slotSource mempool.SlotSource
	mux        *http.ServeMux
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

func NewServerWithSlotSource(store *mempool.Store, builder *inclusion.Builder, slotSource mempool.SlotSource) *Server {
	s := NewServer(store, builder)
	s.slotSource = slotSource
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

func (s *Server) handleInclusionList(w http.ResponseWriter, r *http.Request) {
	snapshot := s.store.Snapshot()
	if s.slotSource != nil {
		slot := uint64(0)
		if raw := r.URL.Query().Get("slot"); raw != "" {
			parsed, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid slot"})
				return
			}
			slot = parsed
		}
		txs, err := s.slotSource.FetchSlot(r.Context(), slot)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		snapshot = mempool.Snapshot{Transactions: txs}
	}
	list := s.builder.Build(snapshot)
	writeJSON(w, http.StatusOK, list)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
