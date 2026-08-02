package app

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"btd/be"
)

const (
	campaignBundleVersion      = "bloc-campaign-bundle-v1"
	campaignBundleManifestFile = "bundle-manifest.json"
	campaignBundleIdentityFile = "cluster-identity.json"
	campaignBundleCRSFile      = "cluster.crs"
	campaignBundleCorpusFile   = "encrypted-corpus.json"
	campaignBundleSecretDir    = "secrets"
)

var (
	campaignSourceSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	campaignECRDigestPattern = regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/[a-z0-9]+(?:[._/-][a-z0-9]+)*@sha256:[0-9a-f]{64}$`)
)

type campaignBundleManifest struct {
	Version                 string            `json:"version"`
	SourceSHA               string            `json:"source_sha"`
	BlocImage               string            `json:"bloc_image"`
	MempoolImage            string            `json:"mempool_image"`
	ClusterConfigVersion    string            `json:"cluster_config_version"`
	CiphertextWireVersion   string            `json:"ciphertext_wire_version"`
	N                       int               `json:"n"`
	Threshold               int               `json:"threshold"`
	BMax                    int               `json:"bmax"`
	PublicConfigID          string            `json:"public_config_id"`
	PlaintextMasterCorpusID string            `json:"plaintext_master_corpus_id"`
	PlaintextPrefixSetIDs   map[string]string `json:"plaintext_prefix_set_ids"`
	EncryptedCorpusID       string            `json:"encrypted_corpus_id"`
	EncryptedPrefixSetIDs   map[string]string `json:"encrypted_prefix_set_ids"`
	IndexAssignment         string            `json:"index_assignment"`
	FileSHA256              map[string]string `json:"file_sha256"`
}

type campaignBundle struct {
	Root         string
	IdentityPath string
	CRSPath      string
	CorpusPath   string
	SecretDir    string
	Identity     campaignIdentity
	Corpus       corpusProvenance
	Manifest     campaignBundleManifest
}

type campaignCorpusDocument struct {
	SchemaVersion           string            `json:"schema_version"`
	CiphertextWireVersion   string            `json:"ciphertext_wire_version"`
	PublicConfigID          string            `json:"public_config_id"`
	PlaintextMasterCorpusID string            `json:"plaintext_master_corpus_id"`
	PlaintextPrefixSetIDs   map[string]string `json:"plaintext_prefix_set_ids"`
	EncryptedCorpusID       string            `json:"encrypted_corpus_id"`
	EncryptedPrefixSetIDs   map[string]string `json:"encrypted_prefix_set_ids"`
	BMax                    int               `json:"bmax"`
	AvailableCount          int               `json:"available_count"`
	IndexAssignment         string            `json:"index_assignment"`
	OrderedIndexSchedule    json.RawMessage   `json:"ordered_index_schedule"`
	ClassCounts             json.RawMessage   `json:"class_counts"`
	Candidates              json.RawMessage   `json:"candidates"`
}

func buildCampaignBundleManifest(root, sourceSHA, blocImage, mempoolImage string) (campaignBundleManifest, error) {
	if err := validateFrozenSourceAndImages(sourceSHA, blocImage, mempoolImage); err != nil {
		return campaignBundleManifest{}, err
	}
	bundle, indexAssignment, err := readCampaignBundlePublicInputs(root)
	if err != nil {
		return campaignBundleManifest{}, err
	}
	fileHashes := map[string]string{}
	for _, name := range []string{campaignBundleIdentityFile, campaignBundleCRSFile, campaignBundleCorpusFile} {
		data, err := os.ReadFile(filepath.Join(bundle.Root, name))
		if err != nil {
			return campaignBundleManifest{}, err
		}
		fileHashes[name] = hashHex(data)
	}
	return campaignBundleManifest{
		Version:                 campaignBundleVersion,
		SourceSHA:               sourceSHA,
		BlocImage:               blocImage,
		MempoolImage:            mempoolImage,
		ClusterConfigVersion:    clusterConfigVersion,
		CiphertextWireVersion:   bundle.Corpus.CiphertextWireVersion,
		N:                       bundle.Identity.N,
		Threshold:               bundle.Identity.Threshold,
		BMax:                    bundle.Identity.BMax,
		PublicConfigID:          bundle.Corpus.PublicConfigID,
		PlaintextMasterCorpusID: bundle.Corpus.PlaintextMasterCorpusID,
		PlaintextPrefixSetIDs:   cloneStringMap(bundle.Corpus.PlaintextPrefixSetIDs),
		EncryptedCorpusID:       bundle.Corpus.EncryptedCorpusID,
		EncryptedPrefixSetIDs:   cloneStringMap(bundle.Corpus.EncryptedPrefixSetIDs),
		IndexAssignment:         indexAssignment,
		FileSHA256:              fileHashes,
	}, nil
}

func loadCampaignBundle(root string) (campaignBundle, error) {
	manifestPath, err := checkedBundleFile(root, campaignBundleManifestFile)
	if err != nil {
		return campaignBundle{}, err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return campaignBundle{}, err
	}
	var manifest campaignBundleManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return campaignBundle{}, fmt.Errorf("decode campaign bundle manifest: %w", err)
	}
	if manifest.Version != campaignBundleVersion {
		return campaignBundle{}, fmt.Errorf("unsupported campaign bundle version %q", manifest.Version)
	}
	if err := validateFrozenSourceAndImages(manifest.SourceSHA, manifest.BlocImage, manifest.MempoolImage); err != nil {
		return campaignBundle{}, err
	}
	for _, name := range []string{campaignBundleIdentityFile, campaignBundleCRSFile, campaignBundleCorpusFile} {
		path, err := checkedBundleFile(root, name)
		if err != nil {
			return campaignBundle{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return campaignBundle{}, err
		}
		if got, want := hashHex(data), manifest.FileSHA256[name]; want == "" || got != want {
			return campaignBundle{}, fmt.Errorf("campaign bundle file hash mismatch for %s: got %s, want %s", name, got, want)
		}
	}
	if len(manifest.FileSHA256) != 3 {
		return campaignBundle{}, fmt.Errorf("campaign bundle file hash set must contain exactly three public files")
	}
	bundle, _, err := readCampaignBundlePublicInputs(root)
	if err != nil {
		return campaignBundle{}, err
	}
	expected, err := buildCampaignBundleManifest(root, manifest.SourceSHA, manifest.BlocImage, manifest.MempoolImage)
	if err != nil {
		return campaignBundle{}, err
	}
	if err := compareCampaignBundleManifests(manifest, expected); err != nil {
		return campaignBundle{}, err
	}
	bundle.Manifest = manifest
	return bundle, nil
}

func readCampaignBundlePublicInputs(root string) (campaignBundle, string, error) {
	rootPath, err := checkedBundleRoot(root)
	if err != nil {
		return campaignBundle{}, "", err
	}
	identityPath, err := checkedBundleFile(rootPath, campaignBundleIdentityFile)
	if err != nil {
		return campaignBundle{}, "", err
	}
	crsPath, err := checkedBundleFile(rootPath, campaignBundleCRSFile)
	if err != nil {
		return campaignBundle{}, "", err
	}
	corpusPath, err := checkedBundleFile(rootPath, campaignBundleCorpusFile)
	if err != nil {
		return campaignBundle{}, "", err
	}
	secretDir, err := checkedBundleDirectory(rootPath, campaignBundleSecretDir)
	if err != nil {
		return campaignBundle{}, "", err
	}
	if info, err := os.Stat(secretDir); err != nil || info.Mode().Perm() != 0700 {
		return campaignBundle{}, "", fmt.Errorf("campaign secret directory must have mode 0700")
	}
	identity, crs, err := readCampaignIdentity(identityPath)
	if err != nil {
		return campaignBundle{}, "", err
	}
	if identity.CRSFile != campaignBundleCRSFile {
		return campaignBundle{}, "", fmt.Errorf("campaign identity CRS must be %s", campaignBundleCRSFile)
	}
	for operatorID := 0; operatorID < identity.N; operatorID++ {
		secretPath, err := checkedBundleFile(secretDir, fmt.Sprintf("operator-%d.json", operatorID))
		if err != nil {
			return campaignBundle{}, "", err
		}
		if info, err := os.Stat(secretPath); err != nil || info.Mode().Perm() != 0600 {
			return campaignBundle{}, "", fmt.Errorf("campaign operator secret %d must have mode 0600", operatorID)
		}
	}
	entries, err := os.ReadDir(secretDir)
	if err != nil {
		return campaignBundle{}, "", err
	}
	if len(entries) != identity.N {
		return campaignBundle{}, "", fmt.Errorf("campaign secret directory has %d entries, want %d", len(entries), identity.N)
	}
	if err := verifyCampaignSecrets(identity, crs, secretDir); err != nil {
		return campaignBundle{}, "", fmt.Errorf("verify campaign secrets: %w", err)
	}
	corpus, indexAssignment, err := readCampaignBundleCorpus(corpusPath)
	if err != nil {
		return campaignBundle{}, "", err
	}
	if err := validatePrimaryCampaignBundle(identity, corpus, indexAssignment); err != nil {
		return campaignBundle{}, "", err
	}
	return campaignBundle{
		Root: rootPath, IdentityPath: identityPath, CRSPath: crsPath,
		CorpusPath: corpusPath, SecretDir: secretDir, Identity: identity, Corpus: corpus,
	}, indexAssignment, nil
}

func readCampaignBundleCorpus(path string) (corpusProvenance, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corpusProvenance{}, "", err
	}
	var document campaignCorpusDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return corpusProvenance{}, "", fmt.Errorf("decode campaign corpus: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return corpusProvenance{}, "", fmt.Errorf("campaign corpus has trailing JSON")
	}
	return corpusProvenance{
		SchemaVersion: document.SchemaVersion, CiphertextWireVersion: document.CiphertextWireVersion,
		PublicConfigID: document.PublicConfigID, PlaintextMasterCorpusID: document.PlaintextMasterCorpusID,
		PlaintextPrefixSetIDs: document.PlaintextPrefixSetIDs, EncryptedCorpusID: document.EncryptedCorpusID,
		EncryptedPrefixSetIDs: document.EncryptedPrefixSetIDs, BMax: document.BMax, AvailableCount: document.AvailableCount,
	}, document.IndexAssignment, nil
}

func validatePrimaryCampaignBundle(identity campaignIdentity, corpus corpusProvenance, indexAssignment string) error {
	if !((identity.N == 4 && identity.Threshold == 3) || (identity.N == 7 && identity.Threshold == 5)) {
		return fmt.Errorf("primary campaign requires n=4,t=3 or n=7,t=5")
	}
	if identity.BMax != 128 || corpus.BMax != 128 || corpus.AvailableCount != 128 {
		return fmt.Errorf("primary campaign requires BMax and corpus availability 128")
	}
	if identity.Blockspace.MaxDecryptedTxs != 128 {
		return fmt.Errorf("primary campaign blockspace must allow exactly 128 transactions")
	}
	if corpus.SchemaVersion != "bloc-encrypted-corpus-v1" || corpus.CiphertextWireVersion != be.LibraryVersion {
		return fmt.Errorf("invalid campaign corpus schema or wire version")
	}
	if indexAssignment != "coordinated-position-v1" {
		return fmt.Errorf("campaign index assignment must be coordinated-position-v1")
	}
	if err := validateExactPrimaryPrefixes("plaintext", corpus.PlaintextPrefixSetIDs); err != nil {
		return err
	}
	if err := validateExactPrimaryPrefixes("encrypted", corpus.EncryptedPrefixSetIDs); err != nil {
		return err
	}
	if corpus.PlaintextMasterCorpusID == "" || corpus.EncryptedCorpusID == "" {
		return fmt.Errorf("campaign corpus identities are required")
	}
	publicKey, err := unmarshalPointHex(newSuite(), identity.PublicKeyHex)
	if err != nil {
		return fmt.Errorf("decode campaign public key: %w", err)
	}
	publicID, err := be.PublicConfigID(identity.BMax, identity.CRSSHA256, publicKey)
	if err != nil {
		return err
	}
	if corpus.PublicConfigID != publicID {
		return fmt.Errorf("campaign corpus public config id %q does not match identity %q", corpus.PublicConfigID, publicID)
	}
	return nil
}

func validateExactPrimaryPrefixes(kind string, values map[string]string) error {
	want := []string{"128", "32", "8"}
	got := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s prefix %s identity is empty", kind, key)
		}
		got = append(got, key)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%s prefix identities must contain exactly 8, 32, and 128", kind)
	}
	return nil
}

func validateFrozenSourceAndImages(sourceSHA, blocImage, mempoolImage string) error {
	if !campaignSourceSHAPattern.MatchString(sourceSHA) {
		return fmt.Errorf("frozen source SHA must be 40 lowercase hexadecimal characters")
	}
	for name, image := range map[string]string{"bloc": blocImage, "mempool": mempoolImage} {
		if !campaignECRDigestPattern.MatchString(image) {
			return fmt.Errorf("%s image must be a complete lowercase private ECR digest reference", name)
		}
	}
	return nil
}

func compareCampaignBundleManifests(got, want campaignBundleManifest) error {
	if got.SourceSHA != want.SourceSHA {
		return fmt.Errorf("campaign source SHA mismatch")
	}
	if got.BlocImage != want.BlocImage || got.MempoolImage != want.MempoolImage {
		return fmt.Errorf("campaign image mismatch")
	}
	if got.PublicConfigID != want.PublicConfigID {
		return fmt.Errorf("campaign public config mismatch")
	}
	if !reflect.DeepEqual(got.PlaintextPrefixSetIDs, want.PlaintextPrefixSetIDs) {
		return fmt.Errorf("campaign plaintext prefix identity mismatch")
	}
	if !reflect.DeepEqual(got.EncryptedPrefixSetIDs, want.EncryptedPrefixSetIDs) {
		return fmt.Errorf("campaign encrypted prefix identity mismatch")
	}
	if got.IndexAssignment != want.IndexAssignment {
		return fmt.Errorf("campaign index assignment mismatch")
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("campaign bundle manifest does not match frozen public inputs")
	}
	return nil
}

func checkedBundleRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("campaign bundle root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("campaign bundle root must be a real directory, not a symlink")
	}
	return filepath.Clean(abs), nil
}

func checkedBundleFile(root, name string) (string, error) {
	if filepath.IsAbs(name) || filepath.Base(name) != name {
		return "", fmt.Errorf("campaign bundle path must be a relative filename: %s", name)
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("campaign bundle file %s must not be a symlink", name)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("campaign bundle file %s is not regular", name)
	}
	return path, nil
}

func checkedBundleDirectory(root, name string) (string, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("campaign bundle directory %s must not be a symlink", name)
	}
	return path, nil
}

func verifyCampaignBundle(args []string) error {
	fs := flag.NewFlagSet("verify-campaign-bundle", flag.ContinueOnError)
	root := fs.String("bundle-root", "", "frozen campaign bundle directory")
	sourceSHA := fs.String("source-sha", "", "expected frozen source SHA")
	blocImage := fs.String("bloc-image", "", "expected digest-addressed private ECR bloc-node image")
	mempoolImage := fs.String("mempool-image", "", "expected digest-addressed private ECR mempool image")
	writeManifest := fs.Bool("write-manifest", false, "write a new manifest after verification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	manifestPath := filepath.Join(*root, campaignBundleManifestFile)
	if *writeManifest {
		if *sourceSHA == "" || *blocImage == "" || *mempoolImage == "" {
			return fmt.Errorf("--write-manifest requires --source-sha, --bloc-image, and --mempool-image")
		}
		if _, err := os.Lstat(manifestPath); err == nil {
			return fmt.Errorf("campaign bundle manifest already exists: %s", manifestPath)
		} else if !os.IsNotExist(err) {
			return err
		}
		manifest, err := buildCampaignBundleManifest(*root, *sourceSHA, *blocImage, *mempoolImage)
		if err != nil {
			return err
		}
		return writeJSONFileAtomic(manifestPath, manifest, 0644)
	}
	bundle, err := loadCampaignBundle(*root)
	if err != nil {
		return err
	}
	if *sourceSHA != "" && bundle.Manifest.SourceSHA != *sourceSHA {
		return fmt.Errorf("campaign source SHA mismatch")
	}
	if *blocImage != "" && bundle.Manifest.BlocImage != *blocImage {
		return fmt.Errorf("campaign bloc image mismatch")
	}
	if *mempoolImage != "" && bundle.Manifest.MempoolImage != *mempoolImage {
		return fmt.Errorf("campaign mempool image mismatch")
	}
	return nil
}
