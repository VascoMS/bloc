package app

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"btd/be"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"go.dedis.ch/kyber/v4/share"
)

const campaignIdentityVersion = "bloc-campaign-identity-v1"

type campaignOperatorIdentity struct {
	ID        uint64 `json:"id"`
	P2PPeerID string `json:"p2p_peer_id"`
}

type campaignIdentity struct {
	Version      string                     `json:"version"`
	ClusterID    string                     `json:"cluster_id"`
	N            int                        `json:"n"`
	Threshold    int                        `json:"threshold"`
	BMax         int                        `json:"bmax"`
	CRSFile      string                     `json:"crs_file"`
	CRSSHA256    string                     `json:"crs_sha256"`
	PublicKeyHex string                     `json:"public_key_hex"`
	Blockspace   BlockspaceConfig           `json:"blockspace"`
	Limits       ResourceLimits             `json:"limits"`
	Operators    []campaignOperatorIdentity `json:"operators"`
}

type campaignIdentityOptions struct {
	ClusterID   string
	IdentityOut string
	CRSOut      string
	SecretsDir  string
	N           int
	Threshold   int
	BMax        int
	Blockspace  BlockspaceConfig
	Limits      ResourceLimits
}

func genCampaignIdentity(args []string) error {
	options := campaignIdentityOptions{}
	fs := flag.NewFlagSet("gen-campaign-identity", flag.ContinueOnError)
	fs.StringVar(&options.ClusterID, "cluster-id", "", "stable campaign cluster identifier")
	fs.IntVar(&options.N, "nodes", 0, "number of operators")
	fs.IntVar(&options.Threshold, "threshold", 0, "BTE decryption threshold")
	fs.IntVar(&options.BMax, "bmax", 0, "BTE domain and maximum encrypted batch")
	fs.StringVar(&options.IdentityOut, "identity-out", "", "network-independent public identity output")
	fs.StringVar(&options.CRSOut, "crs-out", "", "public CRS output")
	fs.StringVar(&options.SecretsDir, "secrets-dir", "", "private per-operator secret directory")
	fs.Uint64Var(&options.Blockspace.MaxDecryptedGas, "max-decrypted-gas", 0, "maximum gas decrypted per slot; zero is uncapped")
	fs.IntVar(&options.Blockspace.MaxDecryptedTxs, "max-decrypted-txs", 0, "maximum transactions decrypted per slot; zero uses BMax")
	fs.Uint64Var(&options.Blockspace.DefaultTxGas, "default-tx-gas", 21000, "default gas assigned to raw transactions")
	fs.IntVar(&options.Limits.MaxProposalBytes, "max-proposal-bytes", defaultMaxProposalBytes, "maximum encoded proposal bytes")
	fs.IntVar(&options.Limits.MaxEnvelopeBytes, "max-envelope-bytes", defaultMaxEnvelopeBytes, "maximum protobuf envelope bytes")
	fs.IntVar(&options.Limits.MaxCombineAttemptsPerSubBatch, "max-combine-attempts-per-sub-batch", defaultMaxCombineAttemptsPerSubBatch, "maximum threshold subset attempts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(options.IdentityOut) == "" || strings.TrimSpace(options.CRSOut) == "" || strings.TrimSpace(options.SecretsDir) == "" {
		return fmt.Errorf("--identity-out, --crs-out, and --secrets-dir are required")
	}
	if err := ensureCampaignIdentityOutputsAbsent(options); err != nil {
		return err
	}
	identity, crs, secrets, err := buildCampaignIdentity(options)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(options.CRSOut, crs, 0644); err != nil {
		return fmt.Errorf("write campaign CRS: %w", err)
	}
	if err := writeJSONFileAtomic(options.IdentityOut, identity, 0644); err != nil {
		return fmt.Errorf("write campaign identity: %w", err)
	}
	if err := ensurePrivateDir(options.SecretsDir); err != nil {
		return fmt.Errorf("create campaign secret directory: %w", err)
	}
	for _, secret := range secrets {
		path := filepath.Join(options.SecretsDir, fmt.Sprintf("operator-%d.json", secret.OperatorID))
		if err := writeJSONFileAtomic(path, secret, 0600); err != nil {
			return fmt.Errorf("write operator %d campaign secret: %w", secret.OperatorID, err)
		}
	}
	return nil
}

func ensureCampaignIdentityOutputsAbsent(options campaignIdentityOptions) error {
	for _, path := range []string{options.IdentityOut, options.CRSOut, options.SecretsDir} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("campaign identity output already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func buildCampaignIdentity(options campaignIdentityOptions) (campaignIdentity, []byte, []NodeSecretConfig, error) {
	if strings.TrimSpace(options.ClusterID) == "" {
		return campaignIdentity{}, nil, nil, fmt.Errorf("campaign cluster id is required")
	}
	if options.N < 4 {
		return campaignIdentity{}, nil, nil, fmt.Errorf("campaign identity requires at least 4 operators")
	}
	if options.Threshold < 1 || options.Threshold > options.N {
		return campaignIdentity{}, nil, nil, fmt.Errorf("campaign threshold must be in [1,%d]", options.N)
	}
	if options.BMax < 1 {
		return campaignIdentity{}, nil, nil, fmt.Errorf("campaign BMax must be positive")
	}
	if options.Limits == (ResourceLimits{}) {
		options.Limits = defaultResourceLimits()
	}
	if err := validateResourceLimits(options.Limits); err != nil {
		return campaignIdentity{}, nil, nil, err
	}
	if options.Blockspace.MaxDecryptedTxs == 0 {
		options.Blockspace.MaxDecryptedTxs = options.BMax
	}
	if options.Blockspace.MaxDecryptedTxs < 1 || options.Blockspace.MaxDecryptedTxs > options.BMax {
		return campaignIdentity{}, nil, nil, fmt.Errorf("max decrypted transactions must be in [1,%d]", options.BMax)
	}
	if options.Blockspace.DefaultTxGas == 0 {
		options.Blockspace.DefaultTxGas = 21000
	}

	suite := newSuite()
	crs, err := be.GeneratePublicCRS(suite, options.BMax)
	if err != nil {
		return campaignIdentity{}, nil, nil, err
	}
	btd, err := be.NewBTDFromPublicCRS(suite, options.BMax, crs)
	if err != nil {
		return campaignIdentity{}, nil, nil, err
	}
	shares, publicKey := btd.KeyGen(options.N, options.Threshold)
	publicKeyHex, err := marshalPointHex(publicKey)
	if err != nil {
		return campaignIdentity{}, nil, nil, err
	}
	crsFile := "cluster.crs"
	if options.IdentityOut != "" && options.CRSOut != "" {
		relative, relErr := filepath.Rel(filepath.Dir(options.IdentityOut), options.CRSOut)
		if relErr != nil {
			return campaignIdentity{}, nil, nil, fmt.Errorf("make campaign CRS path relative: %w", relErr)
		}
		crsFile = filepath.ToSlash(relative)
	}
	identity := campaignIdentity{
		Version:      campaignIdentityVersion,
		ClusterID:    options.ClusterID,
		N:            options.N,
		Threshold:    options.Threshold,
		BMax:         options.BMax,
		CRSFile:      crsFile,
		CRSSHA256:    hashHex(crs),
		PublicKeyHex: publicKeyHex,
		Blockspace:   options.Blockspace,
		Limits:       options.Limits,
		Operators:    make([]campaignOperatorIdentity, 0, options.N),
	}
	secrets := make([]NodeSecretConfig, 0, options.N)
	for operatorID := 0; operatorID < options.N; operatorID++ {
		privateKeyHex, peerID, err := generateLibP2PIdentity()
		if err != nil {
			return campaignIdentity{}, nil, nil, err
		}
		shareHex, err := marshalScalarHex(shares[operatorID].V)
		if err != nil {
			return campaignIdentity{}, nil, nil, err
		}
		identity.Operators = append(identity.Operators, campaignOperatorIdentity{ID: uint64(operatorID), P2PPeerID: peerID})
		secrets = append(secrets, NodeSecretConfig{
			Version:           nodeSecretVersion,
			ClusterID:         options.ClusterID,
			OperatorID:        uint64(operatorID),
			BTEShareScalarHex: shareHex,
			P2PPrivateKeyHex:  privateKeyHex,
		})
	}
	return identity, crs, secrets, nil
}

func readCampaignIdentity(path string) (campaignIdentity, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return campaignIdentity{}, nil, err
	}
	var identity campaignIdentity
	if err := decodeStrictJSON(data, &identity); err != nil {
		return campaignIdentity{}, nil, fmt.Errorf("decode campaign identity: %w", err)
	}
	if err := validateCampaignIdentity(identity); err != nil {
		return campaignIdentity{}, nil, err
	}
	crsPath := identity.CRSFile
	if !filepath.IsAbs(crsPath) {
		crsPath = filepath.Join(filepath.Dir(path), crsPath)
	}
	crs, err := os.ReadFile(crsPath)
	if err != nil {
		return campaignIdentity{}, nil, fmt.Errorf("read campaign CRS: %w", err)
	}
	if got := hashHex(crs); got != identity.CRSSHA256 {
		return campaignIdentity{}, nil, fmt.Errorf("campaign CRS hash mismatch: got %s, want %s", got, identity.CRSSHA256)
	}
	suite := newSuite()
	if _, err := be.NewBTDFromPublicCRS(suite, identity.BMax, crs); err != nil {
		return campaignIdentity{}, nil, fmt.Errorf("decode campaign CRS: %w", err)
	}
	if _, err := unmarshalPointHex(suite, identity.PublicKeyHex); err != nil {
		return campaignIdentity{}, nil, fmt.Errorf("decode campaign public key: %w", err)
	}
	return identity, crs, nil
}

func validateCampaignIdentity(identity campaignIdentity) error {
	if identity.Version != campaignIdentityVersion {
		return fmt.Errorf("unsupported campaign identity version %q", identity.Version)
	}
	if strings.TrimSpace(identity.ClusterID) == "" || identity.N < 4 || identity.Threshold < 1 || identity.Threshold > identity.N || identity.BMax < 1 {
		return fmt.Errorf("invalid campaign identity parameters")
	}
	if strings.TrimSpace(identity.CRSFile) == "" || filepath.IsAbs(identity.CRSFile) || strings.TrimSpace(identity.CRSSHA256) == "" || strings.TrimSpace(identity.PublicKeyHex) == "" {
		return fmt.Errorf("campaign identity requires relative CRS and public key fields")
	}
	if err := validateResourceLimits(identity.Limits); err != nil {
		return err
	}
	if identity.Blockspace.MaxDecryptedTxs < 1 || identity.Blockspace.MaxDecryptedTxs > identity.BMax || identity.Blockspace.DefaultTxGas == 0 {
		return fmt.Errorf("invalid campaign blockspace limits")
	}
	if len(identity.Operators) != identity.N {
		return fmt.Errorf("campaign identity has %d operators, want %d", len(identity.Operators), identity.N)
	}
	seenPeers := map[string]bool{}
	for index, operator := range identity.Operators {
		if operator.ID != uint64(index) || strings.TrimSpace(operator.P2PPeerID) == "" || seenPeers[operator.P2PPeerID] {
			return fmt.Errorf("invalid campaign operator identity at position %d", index)
		}
		seenPeers[operator.P2PPeerID] = true
	}
	return nil
}

func verifyCampaignSecrets(identity campaignIdentity, crs []byte, secretDir string) error {
	if err := validateCampaignIdentity(identity); err != nil {
		return err
	}
	entries, err := os.ReadDir(secretDir)
	if err != nil {
		return err
	}
	if len(entries) != identity.N {
		return fmt.Errorf("campaign secret directory has %d entries, want %d", len(entries), identity.N)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	suite := newSuite()
	if _, err := be.NewBTDFromPublicCRS(suite, identity.BMax, crs); err != nil {
		return fmt.Errorf("decode campaign CRS: %w", err)
	}
	privateShares := make([]*share.PriShare, 0, identity.N)
	for operatorID := 0; operatorID < identity.N; operatorID++ {
		expectedName := fmt.Sprintf("operator-%d.json", operatorID)
		path := filepath.Join(secretDir, expectedName)
		secret, err := readNodeSecrets(path)
		if err != nil {
			return fmt.Errorf("read campaign secret %d: %w", operatorID, err)
		}
		if secret.ClusterID != identity.ClusterID || secret.OperatorID != uint64(operatorID) {
			return fmt.Errorf("campaign secret %d identity mismatch", operatorID)
		}
		peerID, err := peerIDFromPrivateKeyHex(secret.P2PPrivateKeyHex)
		if err != nil {
			return fmt.Errorf("decode campaign secret %d peer id: %w", operatorID, err)
		}
		if peerID != identity.Operators[operatorID].P2PPeerID {
			return fmt.Errorf("campaign secret %d peer id %q does not match %q", operatorID, peerID, identity.Operators[operatorID].P2PPeerID)
		}
		scalar, err := unmarshalScalarHex(suite, secret.BTEShareScalarHex)
		if err != nil {
			return fmt.Errorf("decode campaign secret %d share: %w", operatorID, err)
		}
		privateShares = append(privateShares, &share.PriShare{I: uint32(operatorID), V: scalar})
	}
	master, err := share.RecoverSecret(suite.G1(), privateShares, identity.Threshold, identity.N)
	if err != nil {
		return fmt.Errorf("recover campaign public key: %w", err)
	}
	recoveredPublic := suite.G1().Point().Mul(master, nil)
	wantPublic, err := unmarshalPointHex(suite, identity.PublicKeyHex)
	if err != nil {
		return fmt.Errorf("decode campaign public key: %w", err)
	}
	if !recoveredPublic.Equal(wantPublic) {
		return fmt.Errorf("campaign secret shares do not match public key")
	}
	return nil
}

func peerIDFromPrivateKeyHex(encoded string) (string, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(encoded, "0x"))
	if err != nil {
		return "", err
	}
	privateKey, err := libp2pcrypto.UnmarshalPrivateKey(raw)
	if err != nil {
		return "", err
	}
	peerID, err := libp2ppeer.IDFromPrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	return peerID.String(), nil
}
