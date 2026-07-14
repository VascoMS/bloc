package inclusion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
)

// HashInclusionList returns the stable identity of an inclusion-list proposal.
// The Hash field is excluded so nodes can recompute and verify list identity.
func HashInclusionList(list InclusionList) string {
	type canonical struct {
		Slot       uint64                 `json:"slot"`
		OperatorID uint64                 `json:"operator_id"`
		Items      []EncryptedPlaceholder `json:"items"`
	}
	data, _ := json.Marshal(canonical{Slot: list.Slot, OperatorID: list.OperatorID, Items: list.Items})
	return hashHex(data)
}

// NewAgreedSet canonicalizes the ACS output independently of the order in
// which a local HoneyBadger instance exposes accepted batches.
func NewAgreedSet(slot uint64, lists []InclusionList) AgreedInclusionSet {
	canonicalLists := append([]InclusionList(nil), lists...)
	for i := range canonicalLists {
		canonicalLists[i].Hash = HashInclusionList(canonicalLists[i])
	}
	sort.Slice(canonicalLists, func(i, j int) bool {
		if canonicalLists[i].Hash != canonicalLists[j].Hash {
			return canonicalLists[i].Hash < canonicalLists[j].Hash
		}
		return canonicalLists[i].OperatorID < canonicalLists[j].OperatorID
	})
	total := 0
	for _, list := range canonicalLists {
		total += len(list.Items)
	}
	type canonical struct {
		Slot  uint64          `json:"slot"`
		Lists []InclusionList `json:"lists"`
	}
	data, _ := json.Marshal(canonical{Slot: slot, Lists: canonicalLists})
	return AgreedInclusionSet{Slot: slot, Lists: canonicalLists, Hash: hashHex(data), TotalItems: total}
}

// Merge deduplicates agreed encrypted placeholders and applies the deterministic
// ordering and blockspace limits that define the decrypted set.
func Merge(slot uint64, lists []InclusionList, blockspace BlockspaceConfig, bmax int) MergedEncryptedSet {
	unique := make(map[string]mergeCandidate)
	validated := make(map[string]EncryptedPlaceholder)
	for _, list := range lists {
		for _, item := range list.Items {
			claimedHash := strings.TrimPrefix(strings.ToLower(item.Hash), "0x")
			if prior, exists := validated[claimedHash]; claimedHash != "" && exists && placeholdersEqual(prior, item) {
				continue
			}
			normalized, fee, ok := normalizePlaceholder(item)
			if !ok {
				continue
			}
			if _, exists := unique[normalized.Hash]; exists {
				continue
			}
			unique[normalized.Hash] = mergeCandidate{item: normalized, fee: fee}
			validated[normalized.Hash] = item
		}
	}
	candidates := make([]mergeCandidate, 0, len(unique))
	for _, candidate := range unique {
		candidates = append(candidates, candidate)
	}
	sortCandidates(candidates)

	maxTxs := EffectiveMaxDecryptedTxs(blockspace, bmax)
	selected := make([]EncryptedPlaceholder, 0, minInt(maxTxs, len(candidates)))
	var selectedGas uint64
	for _, candidate := range candidates {
		item := candidate.item
		if len(selected) >= maxTxs {
			break
		}
		if blockspace.MaxDecryptedGas > 0 {
			if item.Gas > blockspace.MaxDecryptedGas-selectedGas {
				continue
			}
		}
		selected = append(selected, item)
		selectedGas += item.Gas
	}
	merged := MergedEncryptedSet{
		Slot:         slot,
		Items:        selected,
		SelectedGas:  selectedGas,
		SkippedItems: len(candidates) - len(selected),
	}
	merged.Hash = hashMergedSet(merged)
	return merged
}

// EncryptedHashes extracts ciphertext identities for the materialized artifact.
func EncryptedHashes(items []EncryptedPlaceholder) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Hash
	}
	return out
}

type mergeCandidate struct {
	item EncryptedPlaceholder
	fee  *big.Int
}

func normalizePlaceholder(item EncryptedPlaceholder) (EncryptedPlaceholder, *big.Int, bool) {
	if len(item.Ciphertext) == 0 || item.Gas == 0 {
		return EncryptedPlaceholder{}, nil, false
	}
	fee, ok := parseBigInt(item.EffectiveFeePerGasWei)
	if !ok {
		return EncryptedPlaceholder{}, nil, false
	}
	computedHash := hashHex(item.Ciphertext)
	if item.Hash == "" {
		item.Hash = computedHash
	}
	normalizedHash := strings.TrimPrefix(strings.ToLower(item.Hash), "0x")
	if normalizedHash != computedHash {
		return EncryptedPlaceholder{}, nil, false
	}
	item.Hash = normalizedHash
	item.From = strings.ToLower(item.From)
	if item.Kind == "" {
		item.Kind = "placeholder"
	}
	if item.EffectiveFeePerGasWei == "" {
		item.EffectiveFeePerGasWei = "0"
	}
	return item, fee, true
}

func sortCandidates(candidates []mergeCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if cmp := candidates[i].fee.Cmp(candidates[j].fee); cmp != 0 {
			return cmp > 0
		}
		left, right := candidates[i].item, candidates[j].item
		if left.From != right.From {
			return left.From < right.From
		}
		if left.Nonce != right.Nonce {
			return left.Nonce < right.Nonce
		}
		return left.Hash < right.Hash
	})
}

func placeholdersEqual(left, right EncryptedPlaceholder) bool {
	return left.Hash == right.Hash &&
		bytes.Equal(left.Ciphertext, right.Ciphertext) &&
		left.Gas == right.Gas &&
		left.EffectiveFeePerGasWei == right.EffectiveFeePerGasWei &&
		left.From == right.From &&
		left.Nonce == right.Nonce &&
		left.Kind == right.Kind
}

func hashMergedSet(merged MergedEncryptedSet) string {
	type canonical struct {
		Slot        uint64                 `json:"slot"`
		Items       []EncryptedPlaceholder `json:"items"`
		SelectedGas uint64                 `json:"selected_gas"`
	}
	data, _ := json.Marshal(canonical{Slot: merged.Slot, Items: merged.Items, SelectedGas: merged.SelectedGas})
	return hashHex(data)
}

// EffectiveMaxDecryptedTxs resolves a zero or oversized transaction cap to bmax.
func EffectiveMaxDecryptedTxs(blockspace BlockspaceConfig, bmax int) int {
	if bmax <= 0 {
		return 0
	}
	if blockspace.MaxDecryptedTxs <= 0 || blockspace.MaxDecryptedTxs > bmax {
		return bmax
	}
	return blockspace.MaxDecryptedTxs
}

func parseBigInt(raw string) (*big.Int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "0"
	}
	out, ok := new(big.Int).SetString(raw, 10)
	if !ok || out.Sign() < 0 {
		return nil, false
	}
	return out, true
}

func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
