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
	var page mempool.SlotPage
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
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		page, err = s.slotSource.FetchSlot(r.Context(), slot, limit)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		snapshot = mempool.Snapshot{Transactions: page.Transactions}
	}
	if s.slotSource != nil {
		list := s.builder.BuildOrdered(snapshot)
		writeJSON(w, http.StatusOK, struct {
			SchemaVersion           string `json:"schema_version"`
			CiphertextWireVersion   string `json:"ciphertext_wire_version"`
			PublicConfigID          string `json:"public_config_id"`
			PlaintextMasterCorpusID string `json:"plaintext_master_corpus_id"`
			EncryptedCorpusID       string `json:"encrypted_corpus_id"`
			EncryptedPrefixSetID    string `json:"encrypted_prefix_set_id"`
			Slot                    uint64 `json:"slot"`
			RequestedCount          int    `json:"requested_count"`
			AvailableCount          int    `json:"available_count"`
			ReturnedCount           int    `json:"returned_count"`
			inclusion.List
		}{
			SchemaVersion:           page.SchemaVersion,
			CiphertextWireVersion:   page.CiphertextWireVersion,
			PublicConfigID:          page.PublicConfigID,
			PlaintextMasterCorpusID: page.PlaintextMasterCorpusID,
			EncryptedCorpusID:       page.EncryptedCorpusID,
			EncryptedPrefixSetID:    page.EncryptedPrefixSetID,
			Slot:                    page.Slot,
			RequestedCount:          page.RequestedCount,
			AvailableCount:          page.AvailableCount,
			ReturnedCount:           list.Count,
			List:                    list,
		})
		return
	}
	list := s.builder.Build(snapshot)
	writeJSON(w, http.StatusOK, list)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
