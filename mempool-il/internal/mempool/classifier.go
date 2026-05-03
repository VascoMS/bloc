package mempool

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

var placeholderSelector = []byte{0x70, 0x68, 0x6c, 0x64} // "phld"

func ClassifyAndParse(tx *Transaction) {
	tx.Kind = TxKindPlaintext
	tx.Placeholder = nil

	p, err := ParsePlaceholderCalldata(tx.Input)
	if err != nil {
		return
	}
	tx.Kind = TxKindPlaceholder
	tx.Placeholder = p
}

func ParsePlaceholderCalldata(input string) (*PlaceholderData, error) {
	hexInput := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input)), "0x")
	if len(hexInput) == 0 {
		return nil, fmt.Errorf("empty calldata")
	}
	b, err := hex.DecodeString(hexInput)
	if err != nil {
		return nil, fmt.Errorf("invalid calldata hex: %w", err)
	}
	if len(b) < 68 {
		return nil, fmt.Errorf("calldata too short")
	}
	if !bytesEqual(b[:4], placeholderSelector) {
		return nil, fmt.Errorf("not a placeholder selector")
	}

	commitment := b[4:36]
	gasWord := b[36:68]
	gas := new(big.Int).SetBytes(gasWord)
	if !gas.IsUint64() {
		return nil, fmt.Errorf("requested gas does not fit uint64")
	}

	return &PlaceholderData{
		CommitmentHex: "0x" + hex.EncodeToString(commitment),
		RequestedGas:  gas.Uint64(),
	}, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
