package app

import (
	"encoding/json"
	"math/big"
	"sort"
	"strings"
)

func hashInclusionList(list InclusionList) string {
	type canonical struct {
		Slot       uint64                 `json:"slot"`
		OperatorID uint64                 `json:"operator_id"`
		Items      []EncryptedPlaceholder `json:"items"`
	}
	data, _ := json.Marshal(canonical{Slot: list.Slot, OperatorID: list.OperatorID, Items: list.Items})
	return hashHex(data)
}

func newAgreedInclusionSet(slot uint64, lists []InclusionList) AgreedInclusionSet {
	canonicalLists := append([]InclusionList(nil), lists...)
	for i := range canonicalLists {
		canonicalLists[i].Hash = hashInclusionList(canonicalLists[i])
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

func mergeInclusionLists(slot uint64, lists []InclusionList, blockspace BlockspaceConfig, bmax int) MergedEncryptedSet {
	unique := make(map[string]EncryptedPlaceholder)
	for _, list := range lists {
		for _, item := range list.Items {
			normalized, ok := normalizePlaceholder(item)
			if !ok {
				continue
			}
			if _, exists := unique[normalized.Hash]; exists {
				continue
			}
			unique[normalized.Hash] = normalized
		}
	}
	candidates := make([]EncryptedPlaceholder, 0, len(unique))
	for _, item := range unique {
		candidates = append(candidates, item)
	}
	sortPlaceholders(candidates)

	maxTxs := effectiveMaxDecryptedTxs(blockspace, bmax)
	selected := make([]EncryptedPlaceholder, 0, minInt(maxTxs, len(candidates)))
	var selectedGas uint64
	for _, item := range candidates {
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
	merged.Hash = hashMergedEncryptedSet(merged)
	return merged
}

func normalizePlaceholder(item EncryptedPlaceholder) (EncryptedPlaceholder, bool) {
	if len(item.Ciphertext) == 0 || item.Gas == 0 {
		return EncryptedPlaceholder{}, false
	}
	if _, ok := parseBigInt(item.EffectiveFeePerGasWei); !ok {
		return EncryptedPlaceholder{}, false
	}
	computedHash := hashHex(item.Ciphertext)
	if item.Hash == "" {
		item.Hash = computedHash
	}
	normalizedHash := strings.TrimPrefix(strings.ToLower(item.Hash), "0x")
	if normalizedHash != computedHash {
		return EncryptedPlaceholder{}, false
	}
	item.Hash = normalizedHash
	item.From = strings.ToLower(item.From)
	if item.Kind == "" {
		item.Kind = "placeholder"
	}
	if item.EffectiveFeePerGasWei == "" {
		item.EffectiveFeePerGasWei = "0"
	}
	return item, true
}

func sortPlaceholders(items []EncryptedPlaceholder) {
	sort.Slice(items, func(i, j int) bool {
		feeI, _ := parseBigInt(items[i].EffectiveFeePerGasWei)
		feeJ, _ := parseBigInt(items[j].EffectiveFeePerGasWei)
		if cmp := feeI.Cmp(feeJ); cmp != 0 {
			return cmp > 0
		}
		if items[i].From != items[j].From {
			return items[i].From < items[j].From
		}
		if items[i].Nonce != items[j].Nonce {
			return items[i].Nonce < items[j].Nonce
		}
		return items[i].Hash < items[j].Hash
	})
}

func hashMergedEncryptedSet(merged MergedEncryptedSet) string {
	type canonical struct {
		Slot        uint64                 `json:"slot"`
		Items       []EncryptedPlaceholder `json:"items"`
		SelectedGas uint64                 `json:"selected_gas"`
	}
	data, _ := json.Marshal(canonical{Slot: merged.Slot, Items: merged.Items, SelectedGas: merged.SelectedGas})
	return hashHex(data)
}

func effectiveMaxDecryptedTxs(blockspace BlockspaceConfig, bmax int) int {
	if bmax <= 0 {
		return 0
	}
	if blockspace.MaxDecryptedTxs <= 0 || blockspace.MaxDecryptedTxs > bmax {
		return bmax
	}
	return blockspace.MaxDecryptedTxs
}

func encryptedHashes(items []EncryptedPlaceholder) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Hash
	}
	return out
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
