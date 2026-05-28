package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// buildInclusionList loads the node's proposal from the configured provider.
func (n *Node) buildInclusionList() (InclusionList, error) {
	switch n.cfg.Provider.Mode {
	case "", "direct":
		n.mu.Lock()
		items := append([]EncryptedPlaceholder(nil), n.pending...)
		n.mu.Unlock()
		list := InclusionList{Slot: n.cfg.Slot, OperatorID: n.self.ID, Items: items}
		list.Hash = hashInclusionList(list)
		return list, nil
	case "mempool-http":
		return n.fetchMempoolInclusionList()
	default:
		return InclusionList{}, fmt.Errorf("unknown inclusion-list provider %q", n.cfg.Provider.Mode)
	}
}

// fetchMempoolInclusionList adapts the standalone mempool-il HTTP response into
// the bloc-node InclusionList type.
func (n *Node) fetchMempoolInclusionList() (InclusionList, error) {
	if n.cfg.Provider.MempoolURL == "" {
		return InclusionList{}, fmt.Errorf("provider mempool-http requires mempool_url")
	}
	resp, err := http.Get(strings.TrimRight(n.cfg.Provider.MempoolURL, "/") + "/inclusion-list")
	if err != nil {
		return InclusionList{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return InclusionList{}, err
	}
	if resp.StatusCode/100 != 2 {
		return InclusionList{}, fmt.Errorf("mempool inclusion-list failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var remote struct {
		Items []struct {
			Hash                   string `json:"hash"`
			CiphertextHex          string `json:"ciphertext_hex"`
			PlaceholderEnvelopeHex string `json:"placeholder_envelope_hex"`
			Gas                    uint64 `json:"gas"`
			EffectiveFeePerGasWei  string `json:"effective_fee_per_gas_wei"`
			From                   string `json:"from"`
			Nonce                  uint64 `json:"nonce"`
			Kind                   string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &remote); err != nil {
		return InclusionList{}, err
	}
	items := make([]EncryptedPlaceholder, 0, len(remote.Items))
	for _, in := range remote.Items {
		if in.Kind != "" && in.Kind != "placeholder" {
			continue
		}
		hexPayload := in.CiphertextHex
		if hexPayload == "" {
			hexPayload = in.PlaceholderEnvelopeHex
		}
		if hexPayload == "" {
			continue
		}
		payload, err := decodeHexMaybe(hexPayload)
		if err != nil {
			return InclusionList{}, fmt.Errorf("decode mempool item %s: %w", in.Hash, err)
		}
		if in.EffectiveFeePerGasWei == "" {
			in.EffectiveFeePerGasWei = "0"
		}
		if in.Kind == "" {
			in.Kind = "placeholder"
		}
		items = append(items, EncryptedPlaceholder{
			Hash:                  hashHex(payload),
			Ciphertext:            payload,
			Gas:                   in.Gas,
			EffectiveFeePerGasWei: in.EffectiveFeePerGasWei,
			From:                  strings.ToLower(in.From),
			Nonce:                 in.Nonce,
			Kind:                  in.Kind,
		})
	}
	list := InclusionList{Slot: n.cfg.Slot, OperatorID: n.self.ID, Items: items}
	list.Hash = hashInclusionList(list)
	return list, nil
}
