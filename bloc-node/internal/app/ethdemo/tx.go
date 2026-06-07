package ethdemo

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

var demoChainID = big.NewInt(1337)

// Summary records stable fields from a syntactically valid signed Ethereum
// transaction used by the local BLOC demo and evaluator.
type Summary struct {
	Hash                  string `json:"hash"`
	From                  string `json:"from"`
	To                    string `json:"to,omitempty"`
	Nonce                 uint64 `json:"nonce"`
	Gas                   uint64 `json:"gas"`
	EffectiveFeePerGasWei string `json:"effective_fee_per_gas_wei"`
	ChainID               string `json:"chain_id"`
	Type                  uint8  `json:"type"`
	SizeBytes             int    `json:"size_bytes"`
}

// Generate creates a deterministic signed EIP-1559 transaction for local MVP
// demos. The transaction is syntactically valid and signed, but the demo does
// not assert that the sender is funded on a live chain.
func Generate(index, minRawBytes int, gas uint64, feeWei string, senderSlot int, nonce uint64) ([]byte, Summary, error) {
	if gas == 0 {
		gas = 21000
	}
	fee, ok := parseBigInt(feeWei)
	if !ok || fee.Sign() == 0 {
		fee = big.NewInt(1)
	}
	tip := new(big.Int).Div(fee, big.NewInt(10))
	if tip.Sign() == 0 {
		tip = big.NewInt(1)
	}
	if tip.Cmp(fee) > 0 {
		tip = new(big.Int).Set(fee)
	}
	key, err := demoPrivateKey(senderSlot)
	if err != nil {
		return nil, Summary{}, err
	}
	to := demoRecipient(index)
	dataLen := 0
	for {
		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   new(big.Int).Set(demoChainID),
			Nonce:     nonce,
			GasTipCap: new(big.Int).Set(tip),
			GasFeeCap: new(big.Int).Set(fee),
			Gas:       maxUint64(gas, intrinsicDemoGas(dataLen)),
			To:        &to,
			Value:     big.NewInt(0),
			Data:      demoCalldata(index, dataLen),
		})
		signed, err := types.SignTx(tx, types.LatestSignerForChainID(demoChainID), key)
		if err != nil {
			return nil, Summary{}, err
		}
		raw, err := signed.MarshalBinary()
		if err != nil {
			return nil, Summary{}, err
		}
		if minRawBytes <= 0 || len(raw) >= minRawBytes {
			summary, err := Parse(raw)
			if err != nil {
				return nil, Summary{}, err
			}
			return raw, summary, nil
		}
		dataLen += minInt(32, minRawBytes-len(raw))
	}
}

// Parse validates that raw is a signed Ethereum transaction and returns the
// stable fields recorded in materialized results.
func Parse(raw []byte) (Summary, error) {
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return Summary{}, err
	}
	chainID := tx.ChainId()
	if chainID == nil || chainID.Sign() == 0 {
		return Summary{}, fmt.Errorf("transaction has no chain id")
	}
	from, err := types.Sender(types.LatestSignerForChainID(chainID), &tx)
	if err != nil {
		return Summary{}, fmt.Errorf("recover sender: %w", err)
	}
	to := ""
	if tx.To() != nil {
		to = tx.To().Hex()
	}
	return Summary{
		Hash:                  tx.Hash().Hex(),
		From:                  from.Hex(),
		To:                    to,
		Nonce:                 tx.Nonce(),
		Gas:                   tx.Gas(),
		EffectiveFeePerGasWei: effectiveFeePerGas(&tx).String(),
		ChainID:               chainID.String(),
		Type:                  tx.Type(),
		SizeBytes:             len(raw),
	}, nil
}

func effectiveFeePerGas(tx *types.Transaction) *big.Int {
	switch tx.Type() {
	case types.DynamicFeeTxType:
		return tx.GasFeeCap()
	default:
		return tx.GasPrice()
	}
}

func demoPrivateKey(slot int) (*ecdsa.PrivateKey, error) {
	for attempt := 0; attempt < 256; attempt++ {
		seed := sha256.Sum256([]byte(fmt.Sprintf("bloc-demo-sender-%d-%d", slot, attempt)))
		key, err := ethcrypto.ToECDSA(seed[:])
		if err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("could not derive demo private key for slot %d", slot)
}

func demoRecipient(index int) common.Address {
	seed := sha256.Sum256([]byte(fmt.Sprintf("bloc-demo-recipient-%d", index)))
	return common.BytesToAddress(seed[12:])
}

func demoCalldata(index, size int) []byte {
	if size <= 0 {
		return nil
	}
	out := make([]byte, size)
	seed, _ := hex.DecodeString(hashHex([]byte(fmt.Sprintf("bloc-demo-calldata-%d", index))))
	for i := range out {
		out[i] = seed[i%len(seed)]
	}
	return out
}

func intrinsicDemoGas(dataLen int) uint64 {
	if dataLen <= 0 {
		return 21000
	}
	return 21000 + uint64(dataLen)*16
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

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
