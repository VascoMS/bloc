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
	Hash                   string `json:"hash"`
	From                   string `json:"from"`
	Nonce                  uint64 `json:"nonce"`
	Gas                    uint64 `json:"gas"`
	Kind                   string `json:"kind"`
	EffectiveFeePerGW      string `json:"effective_fee_per_gas_wei"`
	PlaceholderEnvelopeHex string `json:"placeholder_envelope_hex,omitempty"`
}

type List struct {
	Items    []Item `json:"items"`
	Count    int    `json:"count"`
	TotalGas uint64 `json:"total_gas"`
	Hash     string `json:"hash"`
}

func (b *Builder) Build(snapshot mempool.Snapshot) List {
	unique := make(map[string]mempool.Transaction, len(snapshot.Transactions))
	for _, tx := range snapshot.Transactions {
		if tx.Hash == "" || tx.Gas == 0 {
			continue
		}
		if _, ok := unique[tx.Hash]; ok {
			continue
		}
		unique[tx.Hash] = tx
	}

	candidates := make([]mempool.Transaction, 0, len(unique))
	for _, tx := range unique {
		candidates = append(candidates, tx)
	}

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
			Hash:                   tx.Hash,
			From:                   tx.From,
			Nonce:                  tx.Nonce,
			Gas:                    tx.Gas,
			Kind:                   string(tx.Kind),
			EffectiveFeePerGW:      fee.String(),
			PlaceholderEnvelopeHex: placeholderEnvelope,
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
