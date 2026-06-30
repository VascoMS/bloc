package inclusion

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"mempool-il/internal/mempool"
)

func TestBuilderExposesMockPlaceholderMetadataWithoutRawTarget(t *testing.T) {
	builder := NewBuilder(Config{MaxTransactions: 4, MaxGas: 1_000_000})
	list := builder.Build(mempool.Snapshot{Transactions: []mempool.Transaction{{
		Hash:                     "0xplaceholder",
		From:                     "0xabc",
		Nonce:                    1,
		Gas:                      50_000,
		GasPriceWei:              big.NewInt(100),
		Kind:                     mempool.TxKindPlaceholder,
		Input:                    "0x70686c64",
		RawTx:                    "0xraw-placeholder-tx",
		TargetTxHash:             "0xtarget",
		TargetTxType:             2,
		TargetTxSizeBytes:        180,
		EncryptedPayloadHex:      "0xencrypted",
		EncryptedPayloadHash:     "0xencryptedhash",
		PlaceholderTxHash:        "0xplaceholder",
		PlaceholderCalldataBytes: 100,
		PlaceholderGasEstimate:   52_000,
	}}})
	if len(list.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(list.Items))
	}
	item := list.Items[0]
	if item.EncryptedPayloadHex != "0xencrypted" || item.TargetTxHash != "0xtarget" {
		t.Fatalf("missing mock placeholder metadata: %+v", item)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "raw_tx") || strings.Contains(string(encoded), "raw-placeholder-tx") {
		t.Fatalf("inclusion item exposed raw transaction material: %s", encoded)
	}
}
