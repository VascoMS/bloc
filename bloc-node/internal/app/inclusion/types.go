package inclusion

// EncryptedPlaceholder is one encrypted transaction placeholder plus ordering
// metadata used by the deterministic merge rule.
type EncryptedPlaceholder struct {
	Hash                  string `json:"hash"`
	Ciphertext            []byte `json:"ciphertext"`
	Gas                   uint64 `json:"gas"`
	EffectiveFeePerGasWei string `json:"effective_fee_per_gas_wei"`
	From                  string `json:"from"`
	Nonce                 uint64 `json:"nonce"`
	Kind                  string `json:"kind"`
}

// InclusionList is one operator's ACS proposal for a slot.
type InclusionList struct {
	Slot       uint64                 `json:"slot"`
	OperatorID uint64                 `json:"operator_id"`
	Items      []EncryptedPlaceholder `json:"items"`
	Hash       string                 `json:"hash"`
}

// AgreedInclusionSet is the canonical set of inclusion lists output by ACS.
type AgreedInclusionSet struct {
	Slot       uint64          `json:"slot"`
	Lists      []InclusionList `json:"lists"`
	Hash       string          `json:"hash"`
	TotalItems int             `json:"total_items"`
}

// MergedEncryptedSet is the deterministic post-ACS encrypted prefix selected
// under the configured blockspace limits.
type MergedEncryptedSet struct {
	Slot         uint64                 `json:"slot"`
	Items        []EncryptedPlaceholder `json:"items"`
	Hash         string                 `json:"hash"`
	SelectedGas  uint64                 `json:"selected_gas"`
	SkippedItems int                    `json:"skipped_items"`
}

// BlockspaceConfig limits how much decrypted transaction material a slot may
// output. These limits are applied after ACS decides the set of inclusion lists.
type BlockspaceConfig struct {
	MaxDecryptedGas uint64 `json:"max_decrypted_gas,omitempty"`
	MaxDecryptedTxs int    `json:"max_decrypted_txs,omitempty"`
	DefaultTxGas    uint64 `json:"default_tx_gas,omitempty"`
}
