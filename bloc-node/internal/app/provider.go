package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"bloc-node/internal/app/inclusion"
	"btd/be"
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
	n.mu.Lock()
	proposalLimit := n.proposalLimit
	n.mu.Unlock()
	requestURL := fmt.Sprintf("%s/inclusion-list?slot=%d", strings.TrimRight(n.cfg.Provider.MempoolURL, "/"), n.id)
	if proposalLimit > 0 {
		requestURL += "&limit=" + strconv.Itoa(proposalLimit)
	}
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
		Items                   []struct {
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
	provenanceRequired := n.cfg.Provider.ExpectedPublicConfigID != "" ||
		n.cfg.Provider.ExpectedPlaintextMasterID != "" ||
		n.cfg.Provider.ExpectedEncryptedCorpusID != "" ||
		n.cfg.Provider.RequireExactCount
	if provenanceRequired && remote.SchemaVersion == "" {
		return InclusionList{}, fmt.Errorf("mempool inclusion-list omitted encrypted corpus provenance")
	}
	if remote.SchemaVersion != "" {
		if remote.SchemaVersion != "bloc-encrypted-corpus-v1" {
			return InclusionList{}, fmt.Errorf("unsupported encrypted corpus schema %q", remote.SchemaVersion)
		}
		if remote.CiphertextWireVersion != be.LibraryVersion {
			return InclusionList{}, fmt.Errorf("ciphertext wire version %q does not match %q", remote.CiphertextWireVersion, be.LibraryVersion)
		}
		if remote.Slot != n.id {
			return InclusionList{}, fmt.Errorf("mempool response slot %d does not match active slot %d", remote.Slot, n.id)
		}
		if proposalLimit > 0 && remote.RequestedCount != proposalLimit {
			return InclusionList{}, fmt.Errorf("mempool requested count %d does not match proposal limit %d", remote.RequestedCount, proposalLimit)
		}
		if remote.AvailableCount < remote.ReturnedCount || remote.ReturnedCount < 0 {
			return InclusionList{}, fmt.Errorf("invalid mempool returned/available counts")
		}
		if n.publicConfigID != "" && remote.PublicConfigID != n.publicConfigID {
			return InclusionList{}, fmt.Errorf("mempool public config id %q does not match loaded setup %q", remote.PublicConfigID, n.publicConfigID)
		}
		if want := n.cfg.Provider.ExpectedPublicConfigID; want != "" && remote.PublicConfigID != want {
			return InclusionList{}, fmt.Errorf("mempool public config id %q does not match expected %q", remote.PublicConfigID, want)
		}
		if want := n.cfg.Provider.ExpectedPlaintextMasterID; want != "" && remote.PlaintextMasterCorpusID != want {
			return InclusionList{}, fmt.Errorf("mempool plaintext master corpus id %q does not match expected %q", remote.PlaintextMasterCorpusID, want)
		}
		if want := n.cfg.Provider.ExpectedEncryptedCorpusID; want != "" && remote.EncryptedCorpusID != want {
			return InclusionList{}, fmt.Errorf("mempool encrypted corpus id %q does not match expected %q", remote.EncryptedCorpusID, want)
		}
		if proposalLimit > 0 {
			if want := n.cfg.Provider.ExpectedEncryptedPrefixSetIDs[strconv.Itoa(proposalLimit)]; want != "" && remote.EncryptedPrefixSetID != want {
				return InclusionList{}, fmt.Errorf("mempool encrypted prefix id %q does not match expected %q", remote.EncryptedPrefixSetID, want)
			}
		}
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
	if remote.SchemaVersion != "" && remote.ReturnedCount != len(items) {
		return InclusionList{}, fmt.Errorf("mempool returned count %d does not match decoded items %d", remote.ReturnedCount, len(items))
	}
	if proposalLimit > 0 && len(items) > proposalLimit {
		return InclusionList{}, fmt.Errorf("mempool returned %d items above proposal limit %d", len(items), proposalLimit)
	}
	if n.cfg.Provider.RequireExactCount && proposalLimit > 0 && len(items) != proposalLimit {
		return InclusionList{}, fmt.Errorf("mempool returned %d items, require exact proposal limit %d", len(items), proposalLimit)
	}
	list := InclusionList{Slot: n.id, OperatorID: n.self.ID, Items: items}
	list.Hash = inclusion.HashInclusionList(list)
	return list, nil
}
