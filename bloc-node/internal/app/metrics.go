package app

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	nodeMetricLabelNames     = []string{"cluster_id", "node_id"}
	phaseMetricLabelNames    = []string{"cluster_id", "node_id", "phase"}
	stageMetricLabelNames    = []string{"cluster_id", "node_id", "stage"}
	protocolMetricLabelNames = []string{"cluster_id", "node_id", "direction", "kind"}
	protocolRejectLabelNames = []string{"cluster_id", "node_id", "direction", "reason"}
	shareRejectLabelNames    = []string{"cluster_id", "node_id", "reason"}
	failureMetricLabelNames  = []string{"cluster_id", "node_id", "reason"}
	httpMetricLabelNames     = []string{"cluster_id", "node_id", "method", "handler", "code"}
	slotStageNames           = []string{"total", "proposal_preparation", "acs", "merge_plan", "acs_output_decode", "agreed_set", "merge", "ciphertext_decode", "batch_plan", "share_generation", "threshold_wait", "combine", "materialization", "commit_to_plaintext"}
	slotPhaseNames           = []slotPhase{slotPrepared, slotRunning, slotCompleted, slotFailed}
)

type nodeMetrics struct {
	registry *prometheus.Registry

	clusterID string
	nodeID    string

	nodeInfo            *prometheus.GaugeVec
	slotPhase           *prometheus.GaugeVec
	slotCurrent         *prometheus.GaugeVec
	slotStartedTotal    *prometheus.CounterVec
	slotCompletedTotal  *prometheus.CounterVec
	slotFailedTotal     *prometheus.CounterVec
	slotResultAvailable *prometheus.GaugeVec
	slotStageDuration   *prometheus.HistogramVec
	slotSelectedTxs     *prometheus.GaugeVec
	slotSelectedGas     *prometheus.GaugeVec
	protocolMessages    *prometheus.CounterVec
	protocolBytes       *prometheus.CounterVec
	protocolRejected    *prometheus.CounterVec
	sharesAccepted      *prometheus.CounterVec
	sharesRejected      *prometheus.CounterVec
	shareSubsetAttempts *prometheus.CounterVec
	httpRequests        *prometheus.CounterVec
	httpDuration        *prometheus.HistogramVec
}

func newNodeMetrics(clusterID string, nodeID uint64) *nodeMetrics {
	m := &nodeMetrics{
		registry:  prometheus.NewRegistry(),
		clusterID: clusterID,
		nodeID:    strconv.FormatUint(nodeID, 10),
		nodeInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bloc_node_info",
			Help: "Static BLOC sidecar node information.",
		}, nodeMetricLabelNames),
		slotPhase: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bloc_slot_phase",
			Help: "Current slot phase as a one-hot gauge.",
		}, phaseMetricLabelNames),
		slotCurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bloc_slot_current",
			Help: "Current slot identifier.",
		}, nodeMetricLabelNames),
		slotStartedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_slot_started_total",
			Help: "Slots started by this sidecar process.",
		}, nodeMetricLabelNames),
		slotCompletedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_slot_completed_total",
			Help: "Slots completed by this sidecar process.",
		}, nodeMetricLabelNames),
		slotFailedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_slot_failed_total",
			Help: "Slots that failed inside this sidecar process, labeled by bounded reason.",
		}, failureMetricLabelNames),
		slotResultAvailable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bloc_slot_result_available",
			Help: "Whether the active slot has a materialized result.",
		}, nodeMetricLabelNames),
		slotStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "bloc_slot_stage_duration_seconds",
			Help:    "Completed slot duration by protocol stage in seconds.",
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, stageMetricLabelNames),
		slotSelectedTxs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bloc_slot_selected_transactions",
			Help: "Selected transaction count for the latest active slot.",
		}, nodeMetricLabelNames),
		slotSelectedGas: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bloc_slot_selected_gas",
			Help: "Selected gas for the latest active slot.",
		}, nodeMetricLabelNames),
		protocolMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_protocol_messages_total",
			Help: "Protocol messages counted by direction and kind.",
		}, protocolMetricLabelNames),
		protocolBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_protocol_message_bytes_total",
			Help: "Protocol message bytes counted by direction and kind.",
		}, protocolMetricLabelNames),
		protocolRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_protocol_envelopes_rejected_total",
			Help: "Rejected protocol envelopes counted by direction and bounded reason.",
		}, protocolRejectLabelNames),
		sharesAccepted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_decryption_shares_accepted_total",
			Help: "Unique decryption shares admitted to bounded candidate storage.",
		}, nodeMetricLabelNames),
		sharesRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_decryption_shares_rejected_total",
			Help: "Rejected decryption shares counted by bounded reason.",
		}, shareRejectLabelNames),
		shareSubsetAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_decryption_share_subset_attempts_total",
			Help: "Cryptographic threshold-share subset attempts.",
		}, nodeMetricLabelNames),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bloc_http_requests_total",
			Help: "HTTP requests counted by method, normalized handler, and status code.",
		}, httpMetricLabelNames),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "bloc_http_request_duration_seconds",
			Help:    "HTTP request duration by method, normalized handler, and status code.",
			Buckets: prometheus.DefBuckets,
		}, httpMetricLabelNames),
	}
	m.registry.MustRegister(
		m.nodeInfo,
		m.slotPhase,
		m.slotCurrent,
		m.slotStartedTotal,
		m.slotCompletedTotal,
		m.slotFailedTotal,
		m.slotResultAvailable,
		m.slotStageDuration,
		m.slotSelectedTxs,
		m.slotSelectedGas,
		m.protocolMessages,
		m.protocolBytes,
		m.protocolRejected,
		m.sharesAccepted,
		m.sharesRejected,
		m.shareSubsetAttempts,
		m.httpRequests,
		m.httpDuration,
	)
	m.nodeInfo.WithLabelValues(m.clusterID, m.nodeID).Set(1)
	m.setCurrentSlot(0)
	m.setPhase(slotPrepared)
	m.setResultAvailable(false)
	m.setSelected(0, 0)
	for _, reason := range []string{"proposal", "acs", "decode", "planning", "share", "combine", "unknown"} {
		m.slotFailedTotal.WithLabelValues(m.clusterID, m.nodeID, reason)
	}
	for _, reason := range []string{"oversize", "decode", "authentication", "payload", "unknown"} {
		m.protocolRejected.WithLabelValues(m.clusterID, m.nodeID, "inbound", reason)
		m.protocolRejected.WithLabelValues(m.clusterID, m.nodeID, "outbound", reason)
	}
	for _, reason := range []string{"membership", "batch", "sub_batch", "encoding", "conflict", "capacity", "unknown"} {
		m.sharesRejected.WithLabelValues(m.clusterID, m.nodeID, reason)
	}
	m.sharesAccepted.WithLabelValues(m.labels()...)
	m.shareSubsetAttempts.WithLabelValues(m.labels()...)
	return m
}

func (m *nodeMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *nodeMetrics) labels() []string {
	return []string{m.clusterID, m.nodeID}
}

func (m *nodeMetrics) setCurrentSlot(slotID uint64) {
	m.slotCurrent.WithLabelValues(m.labels()...).Set(float64(slotID))
}

func (m *nodeMetrics) setPhase(phase slotPhase) {
	for _, candidate := range slotPhaseNames {
		value := 0.0
		if phase == candidate {
			value = 1
		}
		m.slotPhase.WithLabelValues(m.clusterID, m.nodeID, string(candidate)).Set(value)
	}
}

func (m *nodeMetrics) setResultAvailable(available bool) {
	value := 0.0
	if available {
		value = 1
	}
	m.slotResultAvailable.WithLabelValues(m.labels()...).Set(value)
}

func (m *nodeMetrics) setSelected(txs int, gas uint64) {
	m.slotSelectedTxs.WithLabelValues(m.labels()...).Set(float64(txs))
	m.slotSelectedGas.WithLabelValues(m.labels()...).Set(float64(gas))
}

func (m *nodeMetrics) slotStarted() {
	m.slotStartedTotal.WithLabelValues(m.labels()...).Inc()
	m.setPhase(slotRunning)
	m.setResultAvailable(false)
}

func (m *nodeMetrics) slotCompleted(snapshot Metrics) {
	m.slotCompletedTotal.WithLabelValues(m.labels()...).Inc()
	m.setPhase(slotCompleted)
	m.setResultAvailable(true)
	m.setSelected(snapshot.SelectedCiphertexts, snapshot.SelectedGas)
	for stage, micros := range map[string]int64{
		"total":                snapshot.TotalSlotUS,
		"proposal_preparation": snapshot.ProposalPreparationUS,
		"acs":                  snapshot.ACSUS,
		"merge_plan":           snapshot.MergePlanUS,
		"acs_output_decode":    snapshot.ACSOutputDecodeUS,
		"agreed_set":           snapshot.AgreedSetUS,
		"merge":                snapshot.MergeUS,
		"ciphertext_decode":    snapshot.CiphertextDecodeUS,
		"batch_plan":           snapshot.BatchPlanUS,
		"share_generation":     snapshot.ShareGenerationUS,
		"threshold_wait":       snapshot.ThresholdWaitUS,
		"combine":              snapshot.CombineUS,
		"materialization":      snapshot.MaterializationUS,
		"commit_to_plaintext":  snapshot.CommitToPlaintextUS,
	} {
		if micros > 0 {
			m.slotStageDuration.WithLabelValues(m.clusterID, m.nodeID, stage).Observe(float64(micros) / float64(time.Second/time.Microsecond))
		}
	}
}

func (m *nodeMetrics) slotFailed(reason string) {
	m.slotFailedTotal.WithLabelValues(m.clusterID, m.nodeID, normalizeFailureReason(reason)).Inc()
	m.setPhase(slotFailed)
	m.setResultAvailable(false)
}

func (m *nodeMetrics) recordProtocol(direction, kind string, size int) {
	normalizedDirection := normalizeDirection(direction)
	normalizedKind := normalizeMessageKind(kind)
	m.protocolMessages.WithLabelValues(m.clusterID, m.nodeID, normalizedDirection, normalizedKind).Inc()
	if size > 0 {
		m.protocolBytes.WithLabelValues(m.clusterID, m.nodeID, normalizedDirection, normalizedKind).Add(float64(size))
	}
}

func (m *nodeMetrics) recordProtocolRejected(direction, reason string) {
	m.protocolRejected.WithLabelValues(m.clusterID, m.nodeID, normalizeDirection(direction), normalizeProtocolRejection(reason)).Inc()
}

func (m *nodeMetrics) recordShareRejected(reason string) {
	m.sharesRejected.WithLabelValues(m.clusterID, m.nodeID, normalizeShareRejection(reason)).Inc()
}

func (m *nodeMetrics) recordShareAccepted() {
	m.sharesAccepted.WithLabelValues(m.labels()...).Inc()
}

func (m *nodeMetrics) recordShareSubsetAttempts(attempts int) {
	if attempts > 0 {
		m.shareSubsetAttempts.WithLabelValues(m.labels()...).Add(float64(attempts))
	}
}

func (m *nodeMetrics) recordHTTP(method, handler string, code int, duration time.Duration) {
	labels := []string{m.clusterID, m.nodeID, normalizeHTTPMethod(method), normalizeHTTPHandler(handler), strconv.Itoa(code)}
	m.httpRequests.WithLabelValues(labels...).Inc()
	m.httpDuration.WithLabelValues(labels...).Observe(duration.Seconds())
}

func normalizeMessageKind(kind string) string {
	switch kind {
	case "acs", "share":
		return kind
	default:
		return "unknown"
	}
}

func normalizeProtocolRejection(reason string) string {
	switch reason {
	case "oversize", "decode", "authentication", "payload":
		return reason
	default:
		return "unknown"
	}
}

func normalizeShareRejection(reason string) string {
	switch reason {
	case "membership", "batch", "sub_batch", "encoding", "conflict", "capacity":
		return reason
	default:
		return "unknown"
	}
}

func normalizeDirection(direction string) string {
	switch direction {
	case "inbound", "outbound":
		return direction
	default:
		return "unknown"
	}
}

func normalizeFailureReason(reason string) string {
	switch reason {
	case "proposal", "acs", "decode", "planning", "share", "combine":
		return reason
	default:
		return "unknown"
	}
}

func normalizeHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost:
		return method
	default:
		return "OTHER"
	}
}

func normalizeHTTPHandler(handler string) string {
	switch handler {
	case "healthz", "tx", "slot_prepare", "slot_status", "metrics", "start", "result":
		return handler
	default:
		return "unknown"
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

func (n *Node) instrumentHTTP(handlerName string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		handler(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		if n.observability != nil {
			n.observability.recordHTTP(r.Method, handlerName, recorder.status, time.Since(start))
		}
	}
}

func (n *Node) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if n.observability == nil {
		http.NotFound(w, r)
		return
	}
	n.observability.handler().ServeHTTP(w, r)
}
