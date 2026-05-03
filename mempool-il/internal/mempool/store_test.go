package mempool

import (
	"math/big"
	"testing"
)

func TestSnapshotDeterministic(t *testing.T) {
	txA := Transaction{Hash: "0x2", From: "0xb", Nonce: 1, Gas: 21000, Kind: TxKindPlaintext, GasPriceWei: big.NewInt(10)}
	txB := Transaction{Hash: "0x1", From: "0xa", Nonce: 0, Gas: 30000, Kind: TxKindPlaceholder, GasPriceWei: big.NewInt(5)}

	s1 := NewStore()
	s1.ReplaceAll([]Transaction{txA, txB})
	a := s1.Snapshot()

	s2 := NewStore()
	s2.ReplaceAll([]Transaction{txB, txA})
	b := s2.Snapshot()

	if a.Hash != b.Hash {
		t.Fatalf("expected equal hashes, got %s != %s", a.Hash, b.Hash)
	}
	if len(a.Transactions) != 2 || a.Transactions[0].Hash != "0x1" || a.Transactions[1].Hash != "0x2" {
		t.Fatalf("unexpected snapshot order")
	}
}
