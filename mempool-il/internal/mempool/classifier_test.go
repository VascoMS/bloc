package mempool

import "testing"

func TestParsePlaceholderCalldata(t *testing.T) {
	input := "0x70686c64" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" +
		"00000000000000000000000000000000000000000000000000000000000186a0"

	p, err := ParsePlaceholderCalldata(input)
	if err != nil {
		t.Fatalf("expected parse success, got %v", err)
	}
	if p.CommitmentHex != "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected commitment: %s", p.CommitmentHex)
	}
	if p.RequestedGas != 100000 {
		t.Fatalf("unexpected gas: %d", p.RequestedGas)
	}
}

func TestClassifyPlaintext(t *testing.T) {
	tx := Transaction{Input: "0xabcdef"}
	ClassifyAndParse(&tx)
	if tx.Kind != TxKindPlaintext {
		t.Fatalf("expected plaintext, got %s", tx.Kind)
	}
	if tx.Placeholder != nil {
		t.Fatalf("expected no placeholder")
	}
}
