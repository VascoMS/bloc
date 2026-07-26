package mempool

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"btd/be"
	"btd/curves"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
)

var mockPlaceholderSelector = []byte{0x70, 0x68, 0x6c, 0x64} // "phld"

// ReplayPlaceholderConfig configures the deterministic mock placeholder source.
type ReplayPlaceholderConfig struct {
	CorpusPath  string
	ClusterPath string
	Slot        uint64
	Loop        bool
}

// ReplayPlaceholderClient turns real signed Ethereum target transactions into
// mock BLOC placeholder candidates. It represents the external submitter side
// of the prototype: target transactions are encrypted once and sidecars only
// consume the resulting encrypted payloads.
type ReplayPlaceholderClient struct {
	targets   []parsedTargetTx
	cluster   replayCluster
	encryptor *be.ClusterBTE
	cache     map[uint64][]Transaction
	mu        sync.Mutex
	slot      uint64
	loop      bool
}

// NewReplayPlaceholderClient loads a target-transaction corpus and encrypts it
// into mock placeholder candidates using public BLOC cluster material.
func NewReplayPlaceholderClient(cfg ReplayPlaceholderConfig) (*ReplayPlaceholderClient, error) {
	if cfg.Slot == 0 {
		cfg.Slot = 1
	}
	cluster, err := readReplayCluster(cfg.ClusterPath)
	if err != nil {
		return nil, err
	}
	targets, err := readTargetCorpus(cfg.CorpusPath)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("corpus %s contained no target transactions", cfg.CorpusPath)
	}
	encryptor, err := newReplayEncryptor(cluster)
	if err != nil {
		return nil, err
	}
	return &ReplayPlaceholderClient{
		targets:   targets,
		cluster:   cluster,
		encryptor: encryptor,
		cache:     map[uint64][]Transaction{},
		slot:      cfg.Slot,
		loop:      cfg.Loop,
	}, nil
}

func (c *ReplayPlaceholderClient) Fetch(ctx context.Context) ([]Transaction, error) {
	return c.FetchSlot(ctx, c.slot)
}

func (c *ReplayPlaceholderClient) FetchSlot(_ context.Context, slot uint64) ([]Transaction, error) {
	if slot == 0 {
		slot = c.slot
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.cache[slot]; ok {
		return append([]Transaction(nil), cached...), nil
	}
	items := make([]Transaction, 0, len(c.targets))
	for i, target := range c.targets {
		item, err := buildMockPlaceholder(target, i, slot, c.cluster, c.encryptor)
		if err != nil {
			return nil, fmt.Errorf("mock placeholder %d: %w", i, err)
		}
		items = append(items, item)
	}
	c.cache[slot] = items
	return append([]Transaction(nil), items...), nil
}

type replayCluster struct {
	Version      string `json:"version"`
	ClusterID    string `json:"cluster_id"`
	BMax         int    `json:"bmax"`
	N            int    `json:"n"`
	Threshold    int    `json:"threshold"`
	CRSFile      string `json:"crs_file"`
	CRSSHA256    string `json:"crs_sha256"`
	PublicKeyHex string `json:"public_key_hex"`
	CRSBytes     []byte `json:"-"`
}

type targetCorpusEntry struct {
	Class corpusClass `json:"class,omitempty"`
	RawTx string      `json:"raw_tx"`
}

type parsedTargetTx struct {
	DeclaredClass corpusClass
	EvidenceClass corpusClass
	Raw           []byte
	Tx            types.Transaction
	Summary       txSummary
}

type txSummary struct {
	Hash                  string
	From                  string
	To                    string
	Nonce                 uint64
	Gas                   uint64
	EffectiveFeePerGasWei string
	Type                  uint8
	SizeBytes             int
}

func readReplayCluster(path string) (replayCluster, error) {
	if strings.TrimSpace(path) == "" {
		return replayCluster{}, fmt.Errorf("replay-placeholder requires -cluster-config")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return replayCluster{}, err
	}
	var cluster replayCluster
	if err := json.Unmarshal(data, &cluster); err != nil {
		return replayCluster{}, err
	}
	if cluster.Version != "bloc-cluster-v2" {
		return replayCluster{}, fmt.Errorf("unsupported cluster config version %q", cluster.Version)
	}
	if cluster.ClusterID == "" || cluster.BMax <= 0 || cluster.CRSFile == "" || cluster.CRSSHA256 == "" || cluster.PublicKeyHex == "" {
		return replayCluster{}, fmt.Errorf("cluster config missing cluster_id, bmax, crs_file, crs_sha256, or public_key_hex")
	}
	crsPath := cluster.CRSFile
	if !filepath.IsAbs(crsPath) {
		crsPath = filepath.Join(filepath.Dir(path), crsPath)
	}
	crs, err := os.ReadFile(crsPath)
	if err != nil {
		return replayCluster{}, fmt.Errorf("read public CRS: %w", err)
	}
	if got := hashHex(crs); !strings.EqualFold(got, strings.TrimSpace(cluster.CRSSHA256)) {
		return replayCluster{}, fmt.Errorf("public CRS hash mismatch: got %s, expected %s", got, cluster.CRSSHA256)
	}
	cluster.CRSBytes = crs
	return cluster, nil
}

func readTargetCorpus(path string) ([]parsedTargetTx, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("replay-placeholder requires -corpus")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []parsedTargetTx
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		rawHex := line
		var declaredClass corpusClass
		if strings.HasPrefix(line, "{") {
			var entry targetCorpusEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return nil, err
			}
			rawHex = entry.RawTx
			declaredClass = entry.Class
		}
		target, err := parseTargetRawTx(rawHex)
		if err != nil {
			return nil, err
		}
		target.DeclaredClass = declaredClass
		out = append(out, target)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseTargetRawTx(rawHex string) (parsedTargetTx, error) {
	raw, err := decodeHexStrict(rawHex)
	if err != nil {
		return parsedTargetTx{}, err
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return parsedTargetTx{}, fmt.Errorf("decode signed ethereum transaction: %w", err)
	}
	chainID := tx.ChainId()
	if chainID == nil || chainID.Sign() == 0 {
		return parsedTargetTx{}, fmt.Errorf("transaction has no chain id")
	}
	from, err := types.Sender(types.LatestSignerForChainID(chainID), &tx)
	if err != nil {
		return parsedTargetTx{}, fmt.Errorf("recover sender: %w", err)
	}
	to := ""
	if tx.To() != nil {
		to = tx.To().Hex()
	}
	return parsedTargetTx{Raw: raw, Tx: tx, Summary: txSummary{
		Hash:                  tx.Hash().Hex(),
		From:                  from.Hex(),
		To:                    to,
		Nonce:                 tx.Nonce(),
		Gas:                   tx.Gas(),
		EffectiveFeePerGasWei: effectiveFeePerGas(&tx).String(),
		Type:                  tx.Type(),
		SizeBytes:             len(raw),
	}}, nil
}

func newReplayEncryptor(cluster replayCluster) (*be.ClusterBTE, error) {
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	btd, err := be.NewBTDFromPublicCRS(suite, cluster.BMax, cluster.CRSBytes)
	if err != nil {
		return nil, fmt.Errorf("load public CRS: %w", err)
	}
	pkBytes, err := hex.DecodeString(strings.TrimPrefix(cluster.PublicKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	pk := suite.G1().Point()
	if err := pk.UnmarshalBinary(pkBytes); err != nil {
		return nil, fmt.Errorf("unmarshal public key: %w", err)
	}
	return be.NewNode(btd, pk, be.SecretShare{}, cluster.N, cluster.Threshold), nil
}

func buildMockPlaceholder(target parsedTargetTx, index int, slot uint64, cluster replayCluster, encryptor *be.ClusterBTE) (Transaction, error) {
	ct, err := encryptor.EncryptTx(target.Raw, index%cluster.BMax, cluster.ClusterID, slot)
	if err != nil {
		return Transaction{}, err
	}
	encoded, err := ct.MarshalBinary()
	if err != nil {
		return Transaction{}, err
	}
	calldata := buildPlaceholderCalldata(target.Summary.Hash, target.Summary.Gas, encoded)
	placeholderRaw, placeholderSummary, err := signMockPlaceholderTx(index, calldata, target.Summary.EffectiveFeePerGasWei)
	if err != nil {
		return Transaction{}, err
	}
	item := Transaction{
		Hash:                   placeholderSummary.Hash,
		From:                   strings.ToLower(placeholderSummary.From),
		To:                     strings.ToLower(placeholderSummary.To),
		Nonce:                  placeholderSummary.Nonce,
		Gas:                    placeholderSummary.Gas,
		Input:                  "0x" + hex.EncodeToString(calldata),
		RawTx:                  "0x" + hex.EncodeToString(placeholderRaw),
		GasPriceWei:            parseDecimalBig(placeholderSummary.EffectiveFeePerGasWei),
		EffectiveFeePerGasW:    parseDecimalBig(placeholderSummary.EffectiveFeePerGasWei),
		TargetTxType:           target.Summary.Type,
		TargetTxSizeBytes:      target.Summary.SizeBytes,
		PlaceholderTxHash:      placeholderSummary.Hash,
		PlaceholderTxRaw:       "0x" + hex.EncodeToString(placeholderRaw),
		PlaceholderGasEstimate: placeholderSummary.Gas,
	}
	ClassifyAndParse(&item)
	if item.Kind != TxKindPlaceholder || item.EncryptedPayloadHex == "" {
		return Transaction{}, fmt.Errorf("generated placeholder transaction did not parse as BLOC placeholder")
	}
	return item, nil
}

func buildPlaceholderCalldata(targetHash string, requestedGas uint64, encryptedPayload []byte) []byte {
	out := make([]byte, 0, 4+32+32+len(encryptedPayload))
	out = append(out, mockPlaceholderSelector...)
	targetHashBytes := common.HexToHash(targetHash).Bytes()
	out = append(out, targetHashBytes...)
	gasWord := make([]byte, 32)
	new(big.Int).SetUint64(requestedGas).FillBytes(gasWord)
	out = append(out, gasWord...)
	out = append(out, encryptedPayload...)
	return out
}

func signMockPlaceholderTx(index int, calldata []byte, feeWei string) ([]byte, txSummary, error) {
	fee := parseDecimalBig(feeWei)
	if fee.Sign() <= 0 {
		fee = big.NewInt(1)
	}
	tip := new(big.Int).Div(fee, big.NewInt(10))
	if tip.Sign() == 0 {
		tip = big.NewInt(1)
	}
	key, err := mockPlaceholderPrivateKey(index)
	if err != nil {
		return nil, txSummary{}, err
	}
	to := common.HexToAddress("0x00000000000000000000000000000000b10c0001")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     uint64(index),
		GasTipCap: tip,
		GasFeeCap: fee,
		Gas:       21000 + uint64(len(calldata))*16,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      calldata,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(1337)), key)
	if err != nil {
		return nil, txSummary{}, err
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return nil, txSummary{}, err
	}
	summary, err := summarizeSignedTx(raw)
	return raw, summary, err
}

func summarizeSignedTx(raw []byte) (txSummary, error) {
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return txSummary{}, err
	}
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), &tx)
	if err != nil {
		return txSummary{}, err
	}
	to := ""
	if tx.To() != nil {
		to = tx.To().Hex()
	}
	return txSummary{
		Hash:                  tx.Hash().Hex(),
		From:                  from.Hex(),
		To:                    to,
		Nonce:                 tx.Nonce(),
		Gas:                   tx.Gas(),
		EffectiveFeePerGasWei: effectiveFeePerGas(&tx).String(),
		Type:                  tx.Type(),
		SizeBytes:             len(raw),
	}, nil
}

func mockPlaceholderPrivateKey(index int) (*ecdsa.PrivateKey, error) {
	for attempt := 0; attempt < 256; attempt++ {
		seed := sha256.Sum256([]byte(fmt.Sprintf("bloc-mock-placeholder-%d-%d", index, attempt)))
		key, err := ethcrypto.ToECDSA(seed[:])
		if err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("could not derive mock placeholder private key")
}

func effectiveFeePerGas(tx *types.Transaction) *big.Int {
	if tx.Type() == types.DynamicFeeTxType {
		return tx.GasFeeCap()
	}
	return tx.GasPrice()
}

func decodeHexStrict(raw string) ([]byte, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "0x"))
	if raw == "" {
		return nil, fmt.Errorf("empty raw transaction")
	}
	if len(raw)%2 == 1 {
		raw = "0" + raw
	}
	return hex.DecodeString(raw)
}

func parseDecimalBig(raw string) *big.Int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return big.NewInt(0)
	}
	out, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return big.NewInt(0)
	}
	return out
}

var _ Source = (*ReplayPlaceholderClient)(nil)
