package mempool

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

var updateClientOverheadCorpus = flag.Bool("update-corpus", false, "rewrite the committed development-only issue #13 client corpus")

func TestCommittedClientOverheadCorpus(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "client-overhead-targets.jsonl")
	expected := generateEvidenceCorpus(t, clientOverheadCorpusClasses)
	if *updateClientOverheadCorpus {
		if err := os.WriteFile(path, expected, 0644); err != nil {
			t.Fatalf("update client overhead corpus: %v", err)
		}
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed client overhead corpus: %v", err)
	}
	if !bytes.Equal(committed, expected) {
		t.Fatalf("committed client corpus differs from deterministic development corpus; run go test ./internal/mempool -run TestCommittedClientOverheadCorpus -count=1 -args -update-corpus")
	}

	targets, err := readClientOverheadCorpus(path)
	if err != nil {
		t.Fatalf("read client overhead corpus: %v", err)
	}
	if got, want := len(targets), 500; got != want {
		t.Fatalf("targets = %d, want %d", got, want)
	}
	assertCorpusCounts(t, targets, clientOverheadCorpusClasses)
}

func TestCommittedProtocolWorkloadCorpus(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "mock-targets.jsonl")
	targets, err := readProtocolWorkloadCorpus(path)
	if err != nil {
		t.Fatalf("read protocol workload corpus: %v", err)
	}
	if got, want := len(targets), 100; got != want {
		t.Fatalf("targets = %d, want %d", got, want)
	}
	assertCorpusCounts(t, targets, protocolWorkloadClasses)
}

func assertCorpusCounts(t *testing.T, targets []parsedTargetTx, specs []corpusClassSpec) {
	t.Helper()
	counts := map[corpusClass]int{}
	hashes := map[string]bool{}
	for _, target := range targets {
		counts[target.EvidenceClass]++
		if hashes[target.Summary.Hash] {
			t.Fatalf("duplicate hash %s", target.Summary.Hash)
		}
		hashes[target.Summary.Hash] = true
	}
	for _, spec := range specs {
		if got := counts[spec.Name]; got != spec.Rows {
			t.Fatalf("%s rows = %d, want %d", spec.Name, got, spec.Rows)
		}
	}
}

func generateEvidenceCorpus(t *testing.T, specs []corpusClassSpec) []byte {
	t.Helper()
	key := evidenceCorpusDevelopmentKey(t)
	to := common.HexToAddress("0x000000000000000000000000000000000000c0de")
	signer := types.LatestSignerForChainID(evidenceCorpusChainID)

	var output bytes.Buffer
	globalIndex := 0
	for _, spec := range specs {
		for classIndex := 0; classIndex < spec.Rows; classIndex++ {
			data := make([]byte, spec.CalldataBytes)
			for byteIndex := range data {
				data[byteIndex] = byte((globalIndex+byteIndex)%255 + 1)
			}
			tx := types.NewTx(&types.DynamicFeeTx{
				ChainID:   new(big.Int).Set(evidenceCorpusChainID),
				Nonce:     uint64(globalIndex),
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(100),
				Gas:       21_000 + uint64(40*len(data)),
				To:        &to,
				Value:     big.NewInt(0),
				Data:      data,
			})
			signed, err := types.SignTx(tx, signer, key)
			if err != nil {
				t.Fatalf("sign corpus transaction %d: %v", globalIndex, err)
			}
			raw, err := signed.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal corpus transaction %d: %v", globalIndex, err)
			}
			line, err := json.Marshal(targetCorpusEntry{
				Class: spec.Name,
				RawTx: "0x" + hex.EncodeToString(raw),
			})
			if err != nil {
				t.Fatalf("marshal corpus entry %d: %v", globalIndex, err)
			}
			output.Write(line)
			output.WriteByte('\n')
			globalIndex++
		}
	}
	return output.Bytes()
}

func evidenceCorpusDevelopmentKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	// This publicly derivable key is test material for chain 1337 only. Never
	// fund or reuse it on a live chain.
	for attempt := 0; attempt < 256; attempt++ {
		seed := sha256.Sum256([]byte(fmt.Sprintf("bloc-issue-13-development-corpus-signer-%d", attempt)))
		key, err := ethcrypto.ToECDSA(seed[:])
		if err == nil {
			return key
		}
	}
	t.Fatal("derive development corpus signer")
	return nil
}

func TestReadEvidenceCorpusRejectsInvalidContracts(t *testing.T) {
	baseEntries := decodeEvidenceCorpusEntries(t, generateEvidenceCorpus(t, clientOverheadCorpusClasses))
	tests := []struct {
		name   string
		mutate func([]targetCorpusEntry)
		want   string
	}{
		{
			name: "duplicate hash",
			mutate: func(entries []targetCorpusEntry) {
				entries[1].RawTx = entries[0].RawTx
			},
			want: "duplicate transaction hash",
		},
		{
			name: "missing label",
			mutate: func(entries []targetCorpusEntry) {
				entries[0].Class = ""
			},
			want: "declared class is missing",
		},
		{
			name: "mismatched label",
			mutate: func(entries []targetCorpusEntry) {
				entries[0].Class = corpusClass128
			},
			want: "does not match",
		},
		{
			name: "wrong chain id",
			mutate: func(entries []targetCorpusEntry) {
				entries[0].RawTx = signedEvidenceRawTx(t, big.NewInt(1), 1000, nil, 21_000)
			},
			want: "chain id",
		},
		{
			name: "wrong distribution",
			mutate: func(entries []targetCorpusEntry) {
				data := bytes.Repeat([]byte{0x01}, 128)
				entries[0] = targetCorpusEntry{
					Class: corpusClass128,
					RawTx: signedEvidenceRawTx(t, evidenceCorpusChainID, 1001, data, 21_000+40*128),
				}
			},
			want: "class distribution",
		},
		{
			name: "underfunded intrinsic gas",
			mutate: func(entries []targetCorpusEntry) {
				entries[0].RawTx = signedEvidenceRawTx(t, evidenceCorpusChainID, 1002, nil, 20_999)
			},
			want: "intrinsic gas",
		},
		{
			name: "underfunded calldata floor",
			mutate: func(entries []targetCorpusEntry) {
				data := bytes.Repeat([]byte{0x01}, 128)
				entries[100].RawTx = signedEvidenceRawTx(t, evidenceCorpusChainID, 1003, data, 21_000+40*128-1)
			},
			want: "intrinsic gas",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries := append([]targetCorpusEntry(nil), baseEntries...)
			test.mutate(entries)
			path := writeEvidenceCorpusEntries(t, entries)
			_, err := readClientOverheadCorpus(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want fragment %q", err, test.want)
			}
		})
	}
}

func decodeEvidenceCorpusEntries(t *testing.T, data []byte) []targetCorpusEntry {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	entries := make([]targetCorpusEntry, 0, len(lines))
	for index, line := range lines {
		var entry targetCorpusEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("decode generated entry %d: %v", index, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func writeEvidenceCorpusEntries(t *testing.T, entries []targetCorpusEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	var output bytes.Buffer
	for index, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal entry %d: %v", index, err)
		}
		output.Write(line)
		output.WriteByte('\n')
	}
	if err := os.WriteFile(path, output.Bytes(), 0644); err != nil {
		t.Fatalf("write evidence corpus: %v", err)
	}
	return path
}

func signedEvidenceRawTx(t *testing.T, chainID *big.Int, nonce uint64, data []byte, gas uint64) string {
	t.Helper()
	to := common.HexToAddress("0x000000000000000000000000000000000000c0de")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   new(big.Int).Set(chainID),
		Nonce:     nonce,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(100),
		Gas:       gas,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), evidenceCorpusDevelopmentKey(t))
	if err != nil {
		t.Fatalf("sign evidence transaction: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal evidence transaction: %v", err)
	}
	return "0x" + hex.EncodeToString(raw)
}
