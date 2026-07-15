package mempool

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"btd/be"
	"btd/curves"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
)

func TestReplayPlaceholderSourceEncryptsCorpusTargets(t *testing.T) {
	dir := t.TempDir()
	cluster, clusterPath, corpusPath, rawTarget := writeReplayFixture(t, dir)

	source, err := NewReplayPlaceholderClient(ReplayPlaceholderConfig{CorpusPath: corpusPath, ClusterPath: clusterPath, Slot: 7})
	if err != nil {
		t.Fatalf("new replay source: %v", err)
	}
	items, err := source.Fetch(t.Context())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.Kind != TxKindPlaceholder {
		t.Fatalf("kind = %q", item.Kind)
	}
	if item.RawTx == strings.ToLower("0x"+hex.EncodeToString(rawTarget)) {
		t.Fatalf("placeholder raw transaction exposed the target raw transaction")
	}
	if item.EncryptedPayloadHex == "" || item.TargetTxHash == "" || item.PlaceholderTxHash == "" {
		t.Fatalf("missing placeholder metadata: %+v", item)
	}
	if item.TargetTxSizeBytes != len(rawTarget) {
		t.Fatalf("target size = %d, want %d", item.TargetTxSizeBytes, len(rawTarget))
	}
	if item.Gas != item.Placeholder.RequestedGas {
		t.Fatalf("gas = %d, want requested gas %d", item.Gas, item.Placeholder.RequestedGas)
	}
	parsed, err := ParsePlaceholderCalldata(item.Input)
	if err != nil {
		t.Fatalf("parse generated placeholder calldata: %v", err)
	}
	if parsed.EncryptedPayloadHex != item.EncryptedPayloadHex {
		t.Fatalf("encrypted payload was not derived from placeholder calldata")
	}

	plaintext := decryptReplayPayload(t, cluster, item.EncryptedPayloadHex)
	if string(plaintext) != string(rawTarget) {
		t.Fatalf("decrypted target mismatch")
	}
}

func writeReplayFixture(t *testing.T, dir string) (*be.ClusterBTE, string, string, []byte) {
	t.Helper()
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	crs, err := be.GeneratePublicCRS(suite, 8)
	if err != nil {
		t.Fatalf("generate public CRS: %v", err)
	}
	btd, err := be.NewBTDFromPublicCRS(suite, 8, crs)
	if err != nil {
		t.Fatalf("load public CRS: %v", err)
	}
	shares, pk := btd.KeyGen(4, 3)
	cluster := be.NewClusterBTE(btd, pk, shares)
	pkBytes, err := pk.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal pk: %v", err)
	}
	clusterDoc := map[string]any{
		"version":        "bloc-cluster-v2",
		"cluster_id":     "replay-test",
		"bmax":           8,
		"n":              4,
		"threshold":      3,
		"crs_file":       "cluster.crs",
		"crs_sha256":     hashHex(crs),
		"public_key_hex": hex.EncodeToString(pkBytes),
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster.crs"), crs, 0644); err != nil {
		t.Fatalf("write CRS: %v", err)
	}
	clusterPath := filepath.Join(dir, "cluster.json")
	data, _ := json.Marshal(clusterDoc)
	if err := os.WriteFile(clusterPath, data, 0644); err != nil {
		t.Fatalf("write cluster: %v", err)
	}
	rawTarget := signedTestTx(t)
	corpusPath := filepath.Join(dir, "corpus.jsonl")
	line := `{"raw_tx":"0x` + hex.EncodeToString(rawTarget) + `"}` + "\n"
	if err := os.WriteFile(corpusPath, []byte(line), 0644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	return cluster, clusterPath, corpusPath, rawTarget
}

func signedTestTx(t *testing.T) []byte {
	t.Helper()
	key, err := ethcrypto.ToECDSA(common.Hex2Bytes("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	to := common.HexToAddress("0x000000000000000000000000000000000000c0de")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     1,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(100),
		Gas:       22000,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      []byte{0x01, 0x02, 0x03},
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(1337)), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func decryptReplayPayload(t *testing.T, cluster *be.ClusterBTE, payloadHex string) []byte {
	t.Helper()
	payload, err := hex.DecodeString(strings.TrimPrefix(payloadHex, "0x"))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	ct, err := cluster.UnmarshalCiphertext(payload)
	if err != nil {
		t.Fatalf("unmarshal ciphertext: %v", err)
	}
	plan, err := cluster.PlanBatch([]be.Ciphertext{ct})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var shares []be.DecryptionShare
	for i := 0; i < 3; i++ {
		share, err := cluster.MakeShare(cluster.Shares[i], plan, 0)
		if err != nil {
			t.Fatalf("share: %v", err)
		}
		shares = append(shares, share)
	}
	results, err := cluster.CombineShares(plan, shares)
	if err != nil {
		t.Fatalf("combine: %v", err)
	}
	if len(results) != 1 || results[0].Err != nil || !results[0].HashOK {
		t.Fatalf("bad results: %+v", results)
	}
	return results[0].RawTx
}
