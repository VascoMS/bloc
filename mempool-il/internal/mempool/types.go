package mempool

import "math/big"

type TxKind string

const (
	TxKindPlaintext   TxKind = "plaintext"
	TxKindPlaceholder TxKind = "placeholder"
)

type PlaceholderData struct {
	CommitmentHex        string `json:"commitment_hex"`
	RequestedGas         uint64 `json:"requested_gas"`
	EncryptedPayloadHex  string `json:"encrypted_payload_hex,omitempty"`
	EncryptedPayloadHash string `json:"encrypted_payload_hash,omitempty"`
	CalldataBytes        int    `json:"calldata_bytes,omitempty"`
}

type Transaction struct {
	Hash                     string           `json:"hash"`
	From                     string           `json:"from"`
	To                       string           `json:"to,omitempty"`
	Nonce                    uint64           `json:"nonce"`
	Gas                      uint64           `json:"gas"`
	Input                    string           `json:"input,omitempty"`
	RawTx                    string           `json:"raw_tx,omitempty"`
	GasPriceWei              *big.Int         `json:"-"`
	MaxFeePerGasWei          *big.Int         `json:"-"`
	MaxPriorityFeeWei        *big.Int         `json:"-"`
	Kind                     TxKind           `json:"kind"`
	Placeholder              *PlaceholderData `json:"placeholder,omitempty"`
	IsQueued                 bool             `json:"is_queued"`
	EffectiveFeePerGasW      *big.Int         `json:"-"`
	TargetTxHash             string           `json:"target_tx_hash,omitempty"`
	TargetTxType             uint8            `json:"target_tx_type,omitempty"`
	TargetTxSizeBytes        int              `json:"target_tx_size_bytes,omitempty"`
	EncryptedPayloadHex      string           `json:"encrypted_payload_hex,omitempty"`
	EncryptedPayloadHash     string           `json:"encrypted_payload_hash,omitempty"`
	PlaceholderTxHash        string           `json:"placeholder_tx_hash,omitempty"`
	PlaceholderTxRaw         string           `json:"placeholder_tx_raw,omitempty"`
	PlaceholderCalldataBytes int              `json:"placeholder_calldata_bytes,omitempty"`
	PlaceholderGasEstimate   uint64           `json:"placeholder_gas_estimate,omitempty"`
}

func (t Transaction) EffectiveFeePerGas() *big.Int {
	if t.EffectiveFeePerGasW != nil {
		return new(big.Int).Set(t.EffectiveFeePerGasW)
	}
	if t.MaxFeePerGasWei != nil && t.MaxFeePerGasWei.Sign() > 0 {
		return new(big.Int).Set(t.MaxFeePerGasWei)
	}
	if t.GasPriceWei != nil {
		return new(big.Int).Set(t.GasPriceWei)
	}
	return big.NewInt(0)
}
