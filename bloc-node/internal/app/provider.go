package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"bloc-node/internal/app/inclusion"
)

// buildInclusionList loads the node's proposal from the configured provider.
func (n *Node) buildInclusionList() (InclusionList, error) {
	switch n.cfg.Provider.Mode {
	case "", "direct":
		n.mu.Lock()
		items := append([]EncryptedPlaceholder(nil), n.pending...)
		n.mu.Unlock()
		list := InclusionList{Slot: n.id, OperatorID: n.self.ID, Items: items}
		list.Hash = inclusion.HashInclusionList(list)
		return n.validateProviderProposal(list)
	case "mempool-http":
		list, err := n.fetchMempoolInclusionList()
		if err != nil {
			return InclusionList{}, err
		}
		return n.validateProviderProposal(list)
	default:
		return InclusionList{}, fmt.Errorf("unknown inclusion-list provider %q", n.cfg.Provider.Mode)
	}
}

func (n *Node) validateProviderProposal(list InclusionList) (InclusionList, error) {
	encoded, err := inclusion.EncodeList(list)
	if err != nil {
		return InclusionList{}, err
	}
	if err := validateProposalBounds(len(list.Items), len(encoded), n.cfg); err != nil {
		return InclusionList{}, err
	}
	return list, nil
}

// fetchMempoolInclusionList adapts the standalone mempool-il HTTP response into
// the bloc-node InclusionList type.
func (n *Node) fetchMempoolInclusionList() (InclusionList, error) {
	return n.fetchMempoolInclusionListContext(context.Background())
}

func (n *Node) fetchMempoolInclusionListContext(ctx context.Context) (InclusionList, error) {
	if n.cfg.Provider.MempoolURL == "" {
		return InclusionList{}, fmt.Errorf("provider mempool-http requires mempool_url")
	}
	if n.mempoolClient == nil {
		return InclusionList{}, fmt.Errorf("provider mempool-http client is not initialized")
	}
	requestURL := fmt.Sprintf("%s/inclusion-list?slot=%d", strings.TrimRight(n.cfg.Provider.MempoolURL, "/"), n.id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return InclusionList{}, fmt.Errorf("create mempool inclusion-list request: %w", err)
	}
	resp, err := n.mempoolClient.Do(req)
	if err != nil {
		return InclusionList{}, fmt.Errorf("fetch mempool inclusion-list: %w", err)
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
			EncryptedPayloadHex    string `json:"encrypted_payload_hex"`
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
		hexPayload := in.EncryptedPayloadHex
		strictHex := hexPayload != ""
		if hexPayload == "" {
			hexPayload = in.CiphertextHex
			strictHex = hexPayload != ""
		}
		if hexPayload == "" {
			hexPayload = in.PlaceholderEnvelopeHex
		}
		if hexPayload == "" {
			continue
		}
		var payload []byte
		var err error
		if strictHex {
			payload, err = decodeHexStrict(hexPayload)
		} else {
			payload, err = decodeHexMaybe(hexPayload)
		}
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
	list := InclusionList{Slot: n.id, OperatorID: n.self.ID, Items: items}
	list.Hash = inclusion.HashInclusionList(list)
	return list, nil
}
