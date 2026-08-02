package mempool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"btd/be"
	"btd/curves"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
	"go.dedis.ch/kyber/v4/share"
)

const (
	encryptedCorpusSchemaVersion = "bloc-encrypted-corpus-v1"
	coordinatedIndexAssignment   = "coordinated-position-v1"
)

type EncryptedCorpusOptions struct {
	PlaintextPath        string
	ClusterConfigPath    string
	CampaignIdentityPath string
	SecretPaths          []string
	Limit                int
	OutputPath           string
}

type corpusCampaignIdentity struct {
	Version      string                   `json:"version"`
	ClusterID    string                   `json:"cluster_id"`
	N            int                      `json:"n"`
	Threshold    int                      `json:"threshold"`
	BMax         int                      `json:"bmax"`
	CRSFile      string                   `json:"crs_file"`
	CRSSHA256    string                   `json:"crs_sha256"`
	PublicKeyHex string                   `json:"public_key_hex"`
	Blockspace   corpusCampaignBlockspace `json:"blockspace"`
	Limits       corpusCampaignLimits     `json:"limits"`
	Operators    []corpusCampaignOperator `json:"operators"`
}

type corpusCampaignBlockspace struct {
	MaxDecryptedGas uint64 `json:"max_decrypted_gas"`
	MaxDecryptedTxs int    `json:"max_decrypted_txs"`
	DefaultTxGas    uint64 `json:"default_tx_gas"`
}

type corpusCampaignLimits struct {
	MaxProposalBytes              int `json:"max_proposal_bytes"`
	MaxEnvelopeBytes              int `json:"max_envelope_bytes"`
	MaxCombineAttemptsPerSubBatch int `json:"max_combine_attempts_per_sub_batch"`
}

type corpusCampaignOperator struct {
	ID        uint64 `json:"id"`
	P2PPeerID string `json:"p2p_peer_id"`
}

type EncryptedCorpusManifest struct {
	SchemaVersion           string                     `json:"schema_version"`
	CiphertextWireVersion   string                     `json:"ciphertext_wire_version"`
	PublicConfigID          string                     `json:"public_config_id"`
	PlaintextMasterCorpusID string                     `json:"plaintext_master_corpus_id"`
	PlaintextPrefixSetIDs   map[string]string          `json:"plaintext_prefix_set_ids"`
	EncryptedCorpusID       string                     `json:"encrypted_corpus_id"`
	EncryptedPrefixSetIDs   map[string]string          `json:"encrypted_prefix_set_ids"`
	BMax                    int                        `json:"bmax"`
	AvailableCount          int                        `json:"available_count"`
	IndexAssignment         string                     `json:"index_assignment"`
	OrderedIndexSchedule    []int                      `json:"ordered_index_schedule"`
	ClassCounts             map[string]map[string]int  `json:"class_counts"`
	Candidates              []EncryptedCorpusCandidate `json:"candidates"`
}

type EncryptedCorpusCandidate struct {
	Position              int         `json:"position"`
	Index                 int         `json:"index"`
	Class                 corpusClass `json:"class"`
	TargetTxHash          string      `json:"target_tx_hash"`
	TargetTxSizeBytes     int         `json:"target_tx_size_bytes"`
	CiphertextHex         string      `json:"ciphertext_hex"`
	CiphertextSHA256      string      `json:"ciphertext_sha256"`
	EffectiveFeePerGasWei string      `json:"effective_fee_per_gas_wei"`
	Transaction           Transaction `json:"transaction"`
}

type encryptedCorpusSecret struct {
	Version           string `json:"version"`
	ClusterID         string `json:"cluster_id"`
	OperatorID        uint32 `json:"operator_id"`
	BTEShareScalarHex string `json:"bte_share_scalar_hex"`
	P2PPrivateKeyHex  string `json:"p2p_private_key_hex,omitempty"`
}

type EncryptedCorpusSource struct {
	manifest *EncryptedCorpusManifest
}

func NewEncryptedCorpusSource(path string) (*EncryptedCorpusSource, error) {
	manifest, err := LoadEncryptedCorpus(path)
	if err != nil {
		return nil, err
	}
	return &EncryptedCorpusSource{manifest: manifest}, nil
}

func (source *EncryptedCorpusSource) Fetch(_ context.Context) ([]Transaction, error) {
	page, err := source.FetchSlot(context.Background(), 0, source.manifest.AvailableCount)
	return page.Transactions, err
}

func (source *EncryptedCorpusSource) FetchSlot(_ context.Context, slot uint64, limit int) (SlotPage, error) {
	if limit <= 0 {
		return SlotPage{}, fmt.Errorf("encrypted corpus limit must be positive")
	}
	if limit > source.manifest.BMax {
		return SlotPage{}, fmt.Errorf("requested limit %d exceeds BMax %d", limit, source.manifest.BMax)
	}
	returned := limit
	if returned > source.manifest.AvailableCount {
		returned = source.manifest.AvailableCount
	}
	transactions := make([]Transaction, returned)
	for i := 0; i < returned; i++ {
		transactions[i] = cloneCorpusTransaction(source.manifest.Candidates[i].Transaction)
	}
	return SlotPage{
		SchemaVersion:           source.manifest.SchemaVersion,
		CiphertextWireVersion:   source.manifest.CiphertextWireVersion,
		PublicConfigID:          source.manifest.PublicConfigID,
		PlaintextMasterCorpusID: source.manifest.PlaintextMasterCorpusID,
		EncryptedCorpusID:       source.manifest.EncryptedCorpusID,
		EncryptedPrefixSetID:    encryptedCandidateSetID(source.manifest.Candidates[:returned]),
		Slot:                    slot,
		RequestedCount:          limit,
		AvailableCount:          source.manifest.AvailableCount,
		ReturnedCount:           returned,
		Transactions:            transactions,
	}, nil
}

func cloneCorpusTransaction(transaction Transaction) Transaction {
	out := transaction
	if transaction.GasPriceWei != nil {
		out.GasPriceWei = new(big.Int).Set(transaction.GasPriceWei)
	}
	if transaction.MaxFeePerGasWei != nil {
		out.MaxFeePerGasWei = new(big.Int).Set(transaction.MaxFeePerGasWei)
	}
	if transaction.MaxPriorityFeeWei != nil {
		out.MaxPriorityFeeWei = new(big.Int).Set(transaction.MaxPriorityFeeWei)
	}
	if transaction.EffectiveFeePerGasW != nil {
		out.EffectiveFeePerGasW = new(big.Int).Set(transaction.EffectiveFeePerGasW)
	}
	if transaction.Placeholder != nil {
		placeholder := *transaction.Placeholder
		out.Placeholder = &placeholder
	}
	return out
}

func GenerateEncryptedCorpus(options EncryptedCorpusOptions) (*EncryptedCorpusManifest, error) {
	if options.Limit <= 0 {
		return nil, fmt.Errorf("encrypted corpus limit must be positive")
	}
	if strings.TrimSpace(options.OutputPath) == "" {
		return nil, fmt.Errorf("encrypted corpus output path is required")
	}
	if _, err := os.Stat(options.OutputPath); err == nil {
		return nil, fmt.Errorf("encrypted corpus output %s already exists", options.OutputPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	clusterConfig, err := readEncryptedCorpusPublicConfig(options)
	if err != nil {
		return nil, err
	}
	if options.Limit > clusterConfig.BMax {
		return nil, fmt.Errorf("encrypted corpus limit %d exceeds BMax %d", options.Limit, clusterConfig.BMax)
	}
	targets, err := readProtocolWorkloadCorpus(options.PlaintextPath)
	if err != nil {
		return nil, err
	}
	if options.Limit > len(targets) {
		return nil, fmt.Errorf("encrypted corpus limit %d exceeds plaintext count %d", options.Limit, len(targets))
	}
	cluster, err := newReplayClusterWithSecrets(clusterConfig, options.SecretPaths)
	if err != nil {
		return nil, err
	}
	publicID, err := be.PublicConfigID(clusterConfig.BMax, clusterConfig.CRSSHA256, cluster.PK.Point)
	if err != nil {
		return nil, fmt.Errorf("derive public config id: %w", err)
	}

	manifest := &EncryptedCorpusManifest{
		SchemaVersion:           encryptedCorpusSchemaVersion,
		CiphertextWireVersion:   be.LibraryVersion,
		PublicConfigID:          publicID,
		PlaintextMasterCorpusID: plaintextSetID(targets),
		PlaintextPrefixSetIDs:   map[string]string{},
		EncryptedPrefixSetIDs:   map[string]string{},
		BMax:                    clusterConfig.BMax,
		AvailableCount:          options.Limit,
		IndexAssignment:         coordinatedIndexAssignment,
		OrderedIndexSchedule:    make([]int, options.Limit),
		ClassCounts:             map[string]map[string]int{},
		Candidates:              make([]EncryptedCorpusCandidate, 0, options.Limit),
	}
	for _, prefix := range protocolWorkloadPrefixes {
		if prefix.Size <= options.Limit {
			manifest.PlaintextPrefixSetIDs[strconv.Itoa(prefix.Size)] = plaintextSetID(targets[:prefix.Size])
			manifest.ClassCounts[strconv.Itoa(prefix.Size)] = stringClassCounts(prefix.Counts)
		}
	}

	ciphertexts := make([]be.Ciphertext, 0, options.Limit)
	for position, target := range targets[:options.Limit] {
		index := position % clusterConfig.BMax
		ct, encryptErr := cluster.EncryptTx(target.Raw, index)
		if encryptErr != nil {
			return nil, fmt.Errorf("encrypt target %d: %w", position, encryptErr)
		}
		encoded, marshalErr := ct.MarshalBinary()
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal target %d: %w", position, marshalErr)
		}
		item, placeholderErr := buildMockPlaceholderFromCiphertext(target, position, encoded)
		if placeholderErr != nil {
			return nil, fmt.Errorf("build placeholder %d: %w", position, placeholderErr)
		}
		manifest.OrderedIndexSchedule[position] = index
		manifest.Candidates = append(manifest.Candidates, EncryptedCorpusCandidate{
			Position:              position,
			Index:                 index,
			Class:                 target.EvidenceClass,
			TargetTxHash:          target.Summary.Hash,
			TargetTxSizeBytes:     target.Summary.SizeBytes,
			CiphertextHex:         "0x" + hex.EncodeToString(encoded),
			CiphertextSHA256:      hashHex(encoded),
			EffectiveFeePerGasWei: item.EffectiveFeePerGas().String(),
			Transaction:           item,
		})
		ciphertexts = append(ciphertexts, ct)
	}
	if err := selfCheckEncryptedCorpus(cluster, ciphertexts, targets[:options.Limit]); err != nil {
		return nil, err
	}
	for _, prefix := range protocolWorkloadPrefixes {
		if prefix.Size <= options.Limit {
			manifest.EncryptedPrefixSetIDs[strconv.Itoa(prefix.Size)] = encryptedCandidateSetID(manifest.Candidates[:prefix.Size])
		}
	}
	manifest.EncryptedCorpusID = encryptedCandidateSetID(manifest.Candidates)
	if err := validateEncryptedCorpusManifest(manifest); err != nil {
		return nil, err
	}
	if err := writeEncryptedCorpusAtomic(options.OutputPath, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func readEncryptedCorpusPublicConfig(options EncryptedCorpusOptions) (replayCluster, error) {
	hasClusterConfig := strings.TrimSpace(options.ClusterConfigPath) != ""
	hasCampaignIdentity := strings.TrimSpace(options.CampaignIdentityPath) != ""
	if hasClusterConfig == hasCampaignIdentity {
		return replayCluster{}, fmt.Errorf("exactly one of cluster config or campaign identity is required")
	}
	if hasClusterConfig {
		return readReplayCluster(options.ClusterConfigPath)
	}
	return readCorpusCampaignIdentity(options.CampaignIdentityPath)
}

func readCorpusCampaignIdentity(path string) (replayCluster, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return replayCluster{}, err
	}
	var identity corpusCampaignIdentity
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return replayCluster{}, fmt.Errorf("decode campaign identity: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return replayCluster{}, fmt.Errorf("trailing campaign identity JSON value")
		}
		return replayCluster{}, err
	}
	if identity.Version != "bloc-campaign-identity-v1" {
		return replayCluster{}, fmt.Errorf("unsupported campaign identity version %q", identity.Version)
	}
	if strings.TrimSpace(identity.ClusterID) == "" || identity.N < 4 || identity.Threshold < 1 || identity.Threshold > identity.N || identity.BMax < 1 {
		return replayCluster{}, fmt.Errorf("invalid campaign identity threshold or parameters")
	}
	if strings.TrimSpace(identity.CRSFile) == "" || filepath.IsAbs(identity.CRSFile) || strings.TrimSpace(identity.CRSSHA256) == "" || strings.TrimSpace(identity.PublicKeyHex) == "" {
		return replayCluster{}, fmt.Errorf("campaign identity requires relative CRS and public key fields")
	}
	if identity.Blockspace.MaxDecryptedTxs < 1 || identity.Blockspace.MaxDecryptedTxs > identity.BMax || identity.Blockspace.DefaultTxGas == 0 {
		return replayCluster{}, fmt.Errorf("invalid campaign identity blockspace")
	}
	if identity.Limits.MaxProposalBytes < 1 || identity.Limits.MaxEnvelopeBytes < identity.Limits.MaxProposalBytes || identity.Limits.MaxCombineAttemptsPerSubBatch < 1 {
		return replayCluster{}, fmt.Errorf("invalid campaign identity resource limits")
	}
	if len(identity.Operators) != identity.N {
		return replayCluster{}, fmt.Errorf("campaign identity has %d operators, want %d", len(identity.Operators), identity.N)
	}
	seenPeers := map[string]bool{}
	for index, operator := range identity.Operators {
		if operator.ID != uint64(index) || strings.TrimSpace(operator.P2PPeerID) == "" || seenPeers[operator.P2PPeerID] {
			return replayCluster{}, fmt.Errorf("invalid campaign operator identity at position %d", index)
		}
		seenPeers[operator.P2PPeerID] = true
	}
	crsPath := filepath.Join(filepath.Dir(path), identity.CRSFile)
	crs, err := os.ReadFile(crsPath)
	if err != nil {
		return replayCluster{}, fmt.Errorf("read campaign CRS: %w", err)
	}
	if got := hashHex(crs); !strings.EqualFold(got, strings.TrimSpace(identity.CRSSHA256)) {
		return replayCluster{}, fmt.Errorf("campaign CRS hash mismatch: got %s, expected %s", got, identity.CRSSHA256)
	}
	return replayCluster{
		Version:      "bloc-cluster-v3",
		ClusterID:    identity.ClusterID,
		BMax:         identity.BMax,
		N:            identity.N,
		Threshold:    identity.Threshold,
		CRSFile:      identity.CRSFile,
		CRSSHA256:    identity.CRSSHA256,
		PublicKeyHex: identity.PublicKeyHex,
		CRSBytes:     crs,
	}, nil
}

func LoadEncryptedCorpus(path string) (*EncryptedCorpusManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest EncryptedCorpusManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing encrypted corpus JSON value")
		}
		return nil, err
	}
	if err := validateEncryptedCorpusManifest(&manifest); err != nil {
		return nil, err
	}
	for i := range manifest.Candidates {
		fee, ok := new(big.Int).SetString(manifest.Candidates[i].EffectiveFeePerGasWei, 10)
		if !ok || fee.Sign() < 0 {
			return nil, fmt.Errorf("candidate %d invalid effective fee", i)
		}
		manifest.Candidates[i].Transaction.GasPriceWei = fee
		manifest.Candidates[i].Transaction.EffectiveFeePerGasW = new(big.Int).Set(fee)
	}
	return &manifest, nil
}

func newReplayClusterWithSecrets(cluster replayCluster, secretPaths []string) (*be.ClusterBTE, error) {
	if len(secretPaths) < cluster.Threshold {
		return nil, fmt.Errorf("encrypted corpus self-check has %d secrets, need threshold %d", len(secretPaths), cluster.Threshold)
	}
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	btd, err := be.NewBTDFromPublicCRS(suite, cluster.BMax, cluster.CRSBytes)
	if err != nil {
		return nil, err
	}
	pkBytes, err := hex.DecodeString(strings.TrimPrefix(cluster.PublicKeyHex, "0x"))
	if err != nil {
		return nil, err
	}
	pk := suite.G1().Point()
	if err := pk.UnmarshalBinary(pkBytes); err != nil {
		return nil, err
	}
	shares := make([]*share.PriShare, 0, len(secretPaths))
	seen := map[uint32]bool{}
	for _, path := range secretPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var document encryptedCorpusSecret
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&document); decodeErr != nil {
			return nil, decodeErr
		}
		if document.Version != "bloc-node-secret-v1" || document.ClusterID != cluster.ClusterID {
			return nil, fmt.Errorf("secret %s does not match cluster", path)
		}
		if seen[document.OperatorID] || int(document.OperatorID) >= cluster.N {
			return nil, fmt.Errorf("invalid or duplicate secret operator %d", document.OperatorID)
		}
		seen[document.OperatorID] = true
		scalarBytes, decodeErr := hex.DecodeString(strings.TrimPrefix(document.BTEShareScalarHex, "0x"))
		if decodeErr != nil {
			return nil, decodeErr
		}
		scalar := suite.G1().Scalar()
		if unmarshalErr := scalar.UnmarshalBinary(scalarBytes); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		shares = append(shares, &share.PriShare{I: document.OperatorID, V: scalar})
	}
	return be.NewClusterBTEFromShares(btd, pk, shares, cluster.N, cluster.Threshold)
}

func selfCheckEncryptedCorpus(cluster *be.ClusterBTE, ciphertexts []be.Ciphertext, targets []parsedTargetTx) error {
	plan, err := cluster.PlanBatch(ciphertexts)
	if err != nil {
		return err
	}
	decryptionShares := make([]be.DecryptionShare, 0, cluster.Params.N*plan.Alpha)
	for subBatchID := range plan.SubBatches {
		for _, secret := range cluster.Shares {
			candidate, shareErr := cluster.MakeShare(secret, plan, subBatchID)
			if shareErr != nil {
				return fmt.Errorf("verify proof for sub-batch %d: %w", subBatchID, shareErr)
			}
			decryptionShares = append(decryptionShares, candidate)
		}
	}
	results, err := cluster.CombineShares(plan, decryptionShares)
	if err != nil {
		return fmt.Errorf("self-decrypt encrypted corpus: %w", err)
	}
	if len(results) != len(targets) {
		return fmt.Errorf("self-decrypt result count %d, want %d", len(results), len(targets))
	}
	for i := range results {
		if results[i].Err != nil || !bytes.Equal(results[i].Plaintext, targets[i].Raw) {
			return fmt.Errorf("self-decrypt target %d mismatch: %v", i, results[i].Err)
		}
	}
	return nil
}

func validateEncryptedCorpusManifest(manifest *EncryptedCorpusManifest) error {
	if manifest.SchemaVersion != encryptedCorpusSchemaVersion {
		return fmt.Errorf("unsupported encrypted corpus schema %q", manifest.SchemaVersion)
	}
	if manifest.CiphertextWireVersion != be.LibraryVersion {
		return fmt.Errorf("unsupported ciphertext wire version %q", manifest.CiphertextWireVersion)
	}
	if manifest.PublicConfigID == "" || manifest.PlaintextMasterCorpusID == "" {
		return fmt.Errorf("encrypted corpus identities are required")
	}
	if manifest.BMax <= 0 || manifest.AvailableCount <= 0 || manifest.AvailableCount > manifest.BMax {
		return fmt.Errorf("invalid encrypted corpus bounds")
	}
	if manifest.IndexAssignment != coordinatedIndexAssignment {
		return fmt.Errorf("unsupported index assignment %q", manifest.IndexAssignment)
	}
	if len(manifest.Candidates) != manifest.AvailableCount || len(manifest.OrderedIndexSchedule) != manifest.AvailableCount {
		return fmt.Errorf("encrypted corpus candidate count mismatch")
	}
	for i, candidate := range manifest.Candidates {
		if candidate.Position != i || candidate.Index != i%manifest.BMax || manifest.OrderedIndexSchedule[i] != candidate.Index {
			return fmt.Errorf("candidate %d violates coordinated index schedule", i)
		}
		encoded, err := decodeHexStrict(candidate.CiphertextHex)
		if err != nil {
			return fmt.Errorf("candidate %d ciphertext: %w", i, err)
		}
		if !bytes.HasPrefix(encoded, []byte(be.LibraryVersion)) || hashHex(encoded) != candidate.CiphertextSHA256 {
			return fmt.Errorf("candidate %d ciphertext identity mismatch", i)
		}
		if candidate.Transaction.EncryptedPayloadHex != candidate.CiphertextHex || candidate.Transaction.TargetTxHash != candidate.TargetTxHash {
			return fmt.Errorf("candidate %d placeholder metadata mismatch", i)
		}
	}
	if got := encryptedCandidateSetID(manifest.Candidates); got != manifest.EncryptedCorpusID {
		return fmt.Errorf("encrypted corpus id mismatch: got %s, want %s", got, manifest.EncryptedCorpusID)
	}
	for size, want := range manifest.EncryptedPrefixSetIDs {
		count, err := strconv.Atoi(size)
		if err != nil || count <= 0 || count > len(manifest.Candidates) {
			return fmt.Errorf("invalid encrypted prefix size %q", size)
		}
		if got := encryptedCandidateSetID(manifest.Candidates[:count]); got != want {
			return fmt.Errorf("encrypted prefix %d id mismatch", count)
		}
	}
	return nil
}

func plaintextSetID(targets []parsedTargetTx) string {
	h := sha256.New()
	writeHashField(h, []byte("bloc-plaintext-set-v1"))
	for _, target := range targets {
		writeHashField(h, target.Raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func encryptedCandidateSetID(candidates []EncryptedCorpusCandidate) string {
	h := sha256.New()
	writeHashField(h, []byte("bloc-encrypted-set-v1"))
	for _, candidate := range candidates {
		_ = binary.Write(h, binary.BigEndian, uint64(candidate.Position))
		_ = binary.Write(h, binary.BigEndian, int64(candidate.Index))
		writeHashField(h, []byte(candidate.Class))
		writeHashField(h, []byte(strings.ToLower(candidate.TargetTxHash)))
		encoded, _ := decodeHexStrict(candidate.CiphertextHex)
		writeHashField(h, encoded)
		writeHashField(h, []byte(strings.ToLower(candidate.Transaction.PlaceholderTxHash)))
		writeHashField(h, []byte(strings.ToLower(candidate.Transaction.PlaceholderTxRaw)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeHashField(writer io.Writer, value []byte) {
	_ = binary.Write(writer, binary.BigEndian, uint64(len(value)))
	_, _ = writer.Write(value)
}

func stringClassCounts(counts map[corpusClass]int) map[string]int {
	out := make(map[string]int, len(counts))
	for class, count := range counts {
		out[string(class)] = count
	}
	return out
}

func writeEncryptedCorpusAtomic(path string, manifest *EncryptedCorpusManifest) error {
	parent := filepath.Dir(path)
	temp, err := os.CreateTemp(parent, ".encrypted-corpus-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("encrypted corpus output %s already exists", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tempPath, path)
}
