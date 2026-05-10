package app

import (
	"sync"
	"time"

	"btd/be"
	"btd/curves"
	"github.com/anthdm/hbbft"
)

type ConfigFile struct {
	ClusterID    string           `json:"cluster_id"`
	BMax         int              `json:"bmax"`
	N            int              `json:"n"`
	Threshold    int              `json:"threshold"`
	Slot         uint64           `json:"slot"`
	CRSSeedHex   string           `json:"crs_seed_hex"`
	Nodes        []NodeConfig     `json:"nodes"`
	PublicKeyHex string           `json:"public_key_hex"`
	Shares       []ShareConfig    `json:"shares"`
	Blockspace   BlockspaceConfig `json:"blockspace,omitempty"`
	Provider     ProviderConfig   `json:"provider,omitempty"`
}

type NodeConfig struct {
	ID            uint64 `json:"id"`
	ConsensusAddr string `json:"consensus_addr"`
	HTTPAddr      string `json:"http_addr"`
}

type ShareConfig struct {
	OperatorID int    `json:"operator_id"`
	ScalarHex  string `json:"scalar_hex"`
}

type BlockspaceConfig struct {
	MaxDecryptedGas uint64 `json:"max_decrypted_gas,omitempty"`
	MaxDecryptedTxs int    `json:"max_decrypted_txs,omitempty"`
	DefaultTxGas    uint64 `json:"default_tx_gas,omitempty"`
}

type ProviderConfig struct {
	Mode       string `json:"mode,omitempty"`
	MempoolURL string `json:"mempool_url,omitempty"`
}

type WireEnvelope struct {
	From  uint64
	Kind  string
	Slot  uint64
	ACS   *hbbft.SlotMessage
	Share *WireShare
}

type WireShare struct {
	OperatorID int
	BatchIDHex string
	SubBatchID int
	PointHex   string
}

type EncryptedPlaceholder struct {
	Hash                  string `json:"hash"`
	Ciphertext            []byte `json:"ciphertext"`
	Gas                   uint64 `json:"gas"`
	EffectiveFeePerGasWei string `json:"effective_fee_per_gas_wei"`
	From                  string `json:"from"`
	Nonce                 uint64 `json:"nonce"`
	Kind                  string `json:"kind"`
}

type InclusionList struct {
	Slot       uint64                 `json:"slot"`
	OperatorID uint64                 `json:"operator_id"`
	Items      []EncryptedPlaceholder `json:"items"`
	Hash       string                 `json:"hash"`
}

type AgreedInclusionSet struct {
	Slot       uint64          `json:"slot"`
	Lists      []InclusionList `json:"lists"`
	Hash       string          `json:"hash"`
	TotalItems int             `json:"total_items"`
}

type MergedEncryptedSet struct {
	Slot         uint64                 `json:"slot"`
	Items        []EncryptedPlaceholder `json:"items"`
	Hash         string                 `json:"hash"`
	SelectedGas  uint64                 `json:"selected_gas"`
	SkippedItems int                    `json:"skipped_items"`
}

type MaterializedTransactionSet struct {
	Slot            uint64   `json:"slot"`
	AgreedSetHash   string   `json:"agreed_set_hash"`
	MergedSetHash   string   `json:"merged_set_hash"`
	SelectedGas     uint64   `json:"selected_gas"`
	EncryptedHashes []string `json:"encrypted_hashes"`
	PlaintextHashes []string `json:"plaintext_hashes"`
	PlaintextsHex   []string `json:"plaintexts_hex"`
}

type SubmitTxRequest struct {
	RawTx                 string `json:"raw_tx"`
	Gas                   uint64 `json:"gas,omitempty"`
	EffectiveFeePerGasWei string `json:"effective_fee_per_gas_wei,omitempty"`
	From                  string `json:"from,omitempty"`
	Nonce                 uint64 `json:"nonce,omitempty"`
	Kind                  string `json:"kind,omitempty"`
}

type Node struct {
	cfg       ConfigFile
	self      NodeConfig
	nodeIDs   []uint64
	peers     map[uint64]NodeConfig
	slot      *hbbft.SlotACS
	cluster   *be.ClusterBTE
	secret    be.SecretShare
	suite     curves.Suite
	startOnce sync.Once
	faults    FaultConfig

	mu          sync.Mutex
	pending     []EncryptedPlaceholder
	seenPending map[string]bool
	plan        be.BatchPlan
	material    MaterializedTransactionSet
	planned     bool
	shares      []be.DecryptionShare
	seenShares  map[string]bool
	result      *Result
	metrics     Metrics
}

type Result struct {
	Slot         uint64                     `json:"slot"`
	NodeID       uint64                     `json:"node_id"`
	BatchID      string                     `json:"batch_id"`
	Ciphertexts  int                        `json:"ciphertexts"`
	Plaintexts   []string                   `json:"plaintexts_hex"`
	Materialized MaterializedTransactionSet `json:"materialized_transaction_set"`
	LatencyMS    int64                      `json:"latency_ms"`
	Metrics      Metrics                    `json:"metrics"`
}

type FaultConfig struct {
	OmitProposal  bool
	WithholdShare bool
	CorruptShare  bool
	Delay         time.Duration
}

type Metrics struct {
	SubmittedTxs        int              `json:"submitted_txs"`
	SubmittedBytes      int              `json:"submitted_bytes"`
	ProposalTxs         int              `json:"proposal_txs"`
	ProposalHash        string           `json:"proposal_hash,omitempty"`
	AgreedLists         int              `json:"agreed_lists"`
	AgreedSetHash       string           `json:"agreed_set_hash,omitempty"`
	AgreedCiphertexts   int              `json:"agreed_ciphertexts"`
	MergedSetHash       string           `json:"merged_set_hash,omitempty"`
	SelectedCiphertexts int              `json:"selected_ciphertexts"`
	SkippedCiphertexts  int              `json:"skipped_ciphertexts"`
	SelectedGas         uint64           `json:"selected_gas"`
	MaxDecryptedGas     uint64           `json:"max_decrypted_gas"`
	MaxDecryptedTxs     int              `json:"max_decrypted_txs"`
	SubBatches          int              `json:"sub_batches"`
	SharesGenerated     int              `json:"shares_generated"`
	SharesAccepted      int              `json:"shares_accepted"`
	SharesNeededPerSub  int              `json:"shares_needed_per_sub_batch"`
	OutboundMessages    map[string]int   `json:"outbound_messages"`
	InboundMessages     map[string]int   `json:"inbound_messages"`
	OutboundBytes       map[string]int64 `json:"outbound_bytes"`
	InboundBytes        map[string]int64 `json:"inbound_bytes"`
	SlotStartUnixNano   int64            `json:"slot_start_unix_nano"`
	ACSDecisionUnixNano int64            `json:"acs_decision_unix_nano"`
	PlanDoneUnixNano    int64            `json:"plan_done_unix_nano"`
	SharesDoneUnixNano  int64            `json:"shares_done_unix_nano"`
	FirstShareUnixNano  int64            `json:"first_share_unix_nano"`
	ThresholdUnixNano   int64            `json:"threshold_unix_nano"`
	CombineDoneUnixNano int64            `json:"combine_done_unix_nano"`
	ACSMS               int64            `json:"acs_ms"`
	PlanMS              int64            `json:"plan_ms"`
	ShareGenerationMS   int64            `json:"share_generation_ms"`
	CommitToPlaintextMS int64            `json:"commit_to_plaintext_ms"`
	TotalSlotMS         int64            `json:"total_slot_ms"`
}
