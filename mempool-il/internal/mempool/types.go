package mempool

import "math/big"

type TxKind string

const (
	TxKindPlaintext   TxKind = "plaintext"
	TxKindPlaceholder TxKind = "placeholder"
)

type PlaceholderData struct {
	CommitmentHex string `json:"commitment_hex"`
	RequestedGas  uint64 `json:"requested_gas"`
}

type Transaction struct {
	Hash                string           `json:"hash"`
	From                string           `json:"from"`
	To                  string           `json:"to,omitempty"`
	Nonce               uint64           `json:"nonce"`
	Gas                 uint64           `json:"gas"`
	Input               string           `json:"input,omitempty"`
	GasPriceWei         *big.Int         `json:"-"`
	MaxFeePerGasWei     *big.Int         `json:"-"`
	MaxPriorityFeeWei   *big.Int         `json:"-"`
	Kind                TxKind           `json:"kind"`
	Placeholder         *PlaceholderData `json:"placeholder,omitempty"`
	IsQueued            bool             `json:"is_queued"`
	EffectiveFeePerGasW *big.Int         `json:"-"`
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
