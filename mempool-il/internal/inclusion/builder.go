package inclusion

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"mempool-il/internal/mempool"
)

type Config struct {
	MaxTransactions int
	MaxGas          uint64
}

type Builder struct {
	cfg Config
}

func NewBuilder(cfg Config) *Builder {
	if cfg.MaxTransactions <= 0 {
		cfg.MaxTransactions = 128
	}
	if cfg.MaxGas == 0 {
		cfg.MaxGas = 15_000_000
	}
	return &Builder{cfg: cfg}
}

type Item struct {
	Hash                     string `json:"hash"`
	From                     string `json:"from"`
	Nonce                    uint64 `json:"nonce"`
	Gas                      uint64 `json:"gas"`
	Kind                     string `json:"kind"`
	EffectiveFeePerGW        string `json:"effective_fee_per_gas_wei"`
	PlaceholderEnvelopeHex   string `json:"placeholder_envelope_hex,omitempty"`
	EncryptedPayloadHex      string `json:"encrypted_payload_hex,omitempty"`
	EncryptedPayloadHash     string `json:"encrypted_payload_hash,omitempty"`
	TargetTxHash             string `json:"target_tx_hash,omitempty"`
	TargetTxType             uint8  `json:"target_tx_type,omitempty"`
	TargetTxSizeBytes        int    `json:"target_tx_size_bytes,omitempty"`
	PlaceholderTxHash        string `json:"placeholder_tx_hash,omitempty"`
	PlaceholderCalldataBytes int    `json:"placeholder_calldata_bytes,omitempty"`
	PlaceholderGasEstimate   uint64 `json:"placeholder_gas_estimate,omitempty"`
}

type List struct {
	Items    []Item `json:"items"`
	Count    int    `json:"count"`
	TotalGas uint64 `json:"total_gas"`
	Hash     string `json:"hash"`
}

func (b *Builder) Build(snapshot mempool.Snapshot) List {
	return b.build(snapshot, true)
}

// BuildOrdered applies inclusion bounds without reordering an already
// canonical encrypted-corpus prefix.
func (b *Builder) BuildOrdered(snapshot mempool.Snapshot) List {
	return b.build(snapshot, false)
}

func (b *Builder) build(snapshot mempool.Snapshot, sortByFee bool) List {
	seen := make(map[string]bool, len(snapshot.Transactions))
	candidates := make([]mempool.Transaction, 0, len(snapshot.Transactions))
	for _, tx := range snapshot.Transactions {
		if tx.Hash == "" || tx.Gas == 0 {
			continue
		}
		if seen[tx.Hash] {
			continue
		}
		seen[tx.Hash] = true
		candidates = append(candidates, tx)
	}

	if sortByFee {
		sort.Slice(candidates, func(i, j int) bool {
			feeI := candidates[i].EffectiveFeePerGas()
			feeJ := candidates[j].EffectiveFeePerGas()
			if cmp := feeI.Cmp(feeJ); cmp != 0 {
				return cmp > 0
			}
			if candidates[i].From != candidates[j].From {
				return candidates[i].From < candidates[j].From
			}
			if candidates[i].Nonce != candidates[j].Nonce {
				return candidates[i].Nonce < candidates[j].Nonce
			}
			return candidates[i].Hash < candidates[j].Hash
		})
	}

	items := make([]Item, 0, minInt(b.cfg.MaxTransactions, len(candidates)))
	var totalGas uint64
	for _, tx := range candidates {
		if len(items) >= b.cfg.MaxTransactions {
			break
		}
		if tx.Gas > b.cfg.MaxGas-totalGas {
			continue
		}
		fee := tx.EffectiveFeePerGas()
		placeholderEnvelope := ""
		if tx.Kind == mempool.TxKindPlaceholder {
			placeholderEnvelope = strings.TrimPrefix(tx.Input, "0x")
		}
		items = append(items, Item{
			Hash:                     tx.Hash,
			From:                     tx.From,
			Nonce:                    tx.Nonce,
			Gas:                      tx.Gas,
			Kind:                     string(tx.Kind),
			EffectiveFeePerGW:        fee.String(),
			PlaceholderEnvelopeHex:   placeholderEnvelope,
			EncryptedPayloadHex:      tx.EncryptedPayloadHex,
			EncryptedPayloadHash:     tx.EncryptedPayloadHash,
			TargetTxHash:             tx.TargetTxHash,
			TargetTxType:             tx.TargetTxType,
			TargetTxSizeBytes:        tx.TargetTxSizeBytes,
			PlaceholderTxHash:        tx.PlaceholderTxHash,
			PlaceholderCalldataBytes: tx.PlaceholderCalldataBytes,
			PlaceholderGasEstimate:   tx.PlaceholderGasEstimate,
		})
		totalGas += tx.Gas
	}

	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := strings.Join([]string{item.Hash, item.From, fmt.Sprintf("%d", item.Nonce), fmt.Sprintf("%d", item.Gas), item.Kind, item.EffectiveFeePerGW}, "|")
		lines = append(lines, line)
	}
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))

	return List{
		Items:    items,
		Count:    len(items),
		TotalGas: totalGas,
		Hash:     "0x" + hex.EncodeToString(h[:]),
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
