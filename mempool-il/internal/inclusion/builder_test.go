package inclusion

import (
	"math/big"
	"testing"

	"mempool-il/internal/mempool"
)

func TestBuildDeterministicAndBounded(t *testing.T) {
	builder := NewBuilder(Config{MaxTransactions: 2, MaxGas: 60_000})

	txs := []mempool.Transaction{
		{Hash: "0xaaa", From: "0x1", Nonce: 1, Gas: 21_000, Kind: mempool.TxKindPlaintext, GasPriceWei: big.NewInt(100)},
		{Hash: "0xbbb", From: "0x2", Nonce: 0, Gas: 40_000, Kind: mempool.TxKindPlaceholder, GasPriceWei: big.NewInt(200)},
		{Hash: "0xccc", From: "0x3", Nonce: 0, Gas: 40_000, Kind: mempool.TxKindPlaintext, GasPriceWei: big.NewInt(150)},
	}

	l1 := builder.Build(mempool.Snapshot{Transactions: txs})
	l2 := builder.Build(mempool.Snapshot{Transactions: []mempool.Transaction{txs[2], txs[0], txs[1]}})

	if l1.Hash != l2.Hash {
		t.Fatalf("expected deterministic hash")
	}
	if l1.Count != 1 {
		t.Fatalf("expected 1 tx due to gas cap and ordering, got %d", l1.Count)
	}
	if l1.Items[0].Hash != "0xbbb" {
		t.Fatalf("expected highest fee tx first")
	}
}
