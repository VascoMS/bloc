package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"btd/be"
)

const (
	testCampaignSourceSHA    = "cccccccccccccccccccccccccccccccccccccccc"
	testCampaignBlocImage    = "123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCampaignMempoolImage = "123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestBuildAndLoadCampaignBundle(t *testing.T) {
	root := writeCampaignBundleFixture(t, 4, 3)
	manifest, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if manifest.N != 4 || manifest.Threshold != 3 || manifest.BMax != 128 {
		t.Fatalf("unexpected primary parameters: %+v", manifest)
	}
	if len(manifest.FileSHA256) != 3 {
		t.Fatalf("public file hashes = %+v", manifest.FileSHA256)
	}
	if err := writeJSONFileAtomic(filepath.Join(root, campaignBundleManifestFile), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	bundle, err := loadCampaignBundle(root)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if bundle.Manifest.PublicConfigID != bundle.Corpus.PublicConfigID {
		t.Fatal("public configuration identity was not bound")
	}
}

func TestCampaignBundleCombineWorkersBoundAndCompatible(t *testing.T) {
	parallelRoot := writeCampaignBundleFixture(t, 4, 3, 512)
	setCampaignBundleCombineWorkers(t, parallelRoot, 2)
	parallelManifest, err := buildCampaignBundleManifest(parallelRoot, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage)
	if err != nil {
		t.Fatal(err)
	}
	if got := parallelManifest.MaxCombineWorkers; got != 2 {
		t.Fatalf("manifest max combine workers = %d, want 2", got)
	}
	writeCampaignBundleManifestForTest(t, parallelRoot, parallelManifest, true)
	bundle, err := loadCampaignBundle(parallelRoot)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Manifest.MaxCombineWorkers != 2 || bundle.Identity.Limits.MaxCombineWorkers != 2 {
		t.Fatalf("parallel worker binding lost: manifest=%d identity=%d", bundle.Manifest.MaxCombineWorkers, bundle.Identity.Limits.MaxCombineWorkers)
	}

	t.Run("mismatch", func(t *testing.T) {
		got := parallelManifest
		got.MaxCombineWorkers = 1
		if err := compareCampaignBundleManifests(got, parallelManifest); err == nil || !strings.Contains(err.Error(), "combine workers") {
			t.Fatalf("worker mismatch error = %v", err)
		}
	})

	t.Run("new-b512-omission", func(t *testing.T) {
		got, err := decodeCampaignBundleManifest(campaignBundleManifestBytesForTest(t, parallelManifest, false))
		if err != nil {
			t.Fatal(err)
		}
		if err := compareCampaignBundleManifests(got, parallelManifest); err == nil || !strings.Contains(err.Error(), "combine workers") {
			t.Fatalf("new B512 omission error = %v", err)
		}
	})

	t.Run("historical-omission", func(t *testing.T) {
		want := parallelManifest
		want.BMax = 128
		want.MaxCombineWorkers = 1
		got, err := decodeCampaignBundleManifest(campaignBundleManifestBytesForTest(t, want, false))
		if err != nil {
			t.Fatal(err)
		}
		if got.MaxCombineWorkers != 1 {
			t.Fatalf("historical max combine workers = %d, want 1", got.MaxCombineWorkers)
		}
		if err := compareCampaignBundleManifests(got, want); err != nil {
			t.Fatalf("historical omission did not match identity default one: %v", err)
		}
	})
}

func TestFinalCampaignBundleValidationAcceptsScaleExtensionInputs(t *testing.T) {
	tests := []struct {
		name      string
		n         int
		threshold int
		bmax      int
	}{
		{name: "n10-small-batches", n: 10, threshold: 7, bmax: 128},
		{name: "n4-batch512", n: 4, threshold: 3, bmax: 512},
		{name: "n7-batch512", n: 7, threshold: 5, bmax: 512},
		{name: "n10-batch512", n: 10, threshold: 7, bmax: 512},
	}
	publicKey := newSuite().G1().Point().Base()
	publicKeyHex, err := marshalPointHex(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			crsSHA256 := strings.Repeat("d", 64)
			publicID, err := be.PublicConfigID(test.bmax, crsSHA256, publicKey)
			if err != nil {
				t.Fatal(err)
			}
			prefixes := map[string]string{"8": "prefix-8", "32": "prefix-32", "128": "prefix-128"}
			encryptedPrefixes := map[string]string{"8": "encrypted-8", "32": "encrypted-32", "128": "encrypted-128"}
			if test.bmax == 512 {
				prefixes["512"] = "prefix-512"
				encryptedPrefixes["512"] = "encrypted-512"
			}
			identity := campaignIdentity{
				N: test.n, Threshold: test.threshold, BMax: test.bmax,
				CRSSHA256: crsSHA256, PublicKeyHex: publicKeyHex,
				Blockspace: BlockspaceConfig{MaxDecryptedTxs: test.bmax},
			}
			corpus := corpusProvenance{
				SchemaVersion: "bloc-encrypted-corpus-v1", CiphertextWireVersion: be.LibraryVersion,
				PublicConfigID: publicID, PlaintextMasterCorpusID: "plaintext-master",
				PlaintextPrefixSetIDs: prefixes, EncryptedCorpusID: "encrypted-master",
				EncryptedPrefixSetIDs: encryptedPrefixes, BMax: test.bmax, AvailableCount: test.bmax,
			}
			if err := validateFinalCampaignBundle(identity, corpus, "coordinated-position-v1"); err != nil {
				t.Fatalf("validate scale-extension bundle: %v", err)
			}
		})
	}
}

func TestVerifyCampaignBundleWritesOnceAndChecksExpectedIdentities(t *testing.T) {
	root := writeCampaignBundleFixture(t, 4, 3)
	writeArgs := []string{
		"--bundle-root", root,
		"--source-sha", testCampaignSourceSHA,
		"--bloc-image", testCampaignBlocImage,
		"--mempool-image", testCampaignMempoolImage,
		"--write-manifest",
	}
	if err := verifyCampaignBundle(writeArgs); err != nil {
		t.Fatalf("write verified manifest: %v", err)
	}
	if err := verifyCampaignBundle([]string{
		"--bundle-root", root,
		"--source-sha", testCampaignSourceSHA,
		"--bloc-image", testCampaignBlocImage,
		"--mempool-image", testCampaignMempoolImage,
	}); err != nil {
		t.Fatalf("verify frozen expectations: %v", err)
	}
	if err := verifyCampaignBundle(writeArgs); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v", err)
	}
	if err := verifyCampaignBundle([]string{"--bundle-root", root, "--source-sha", strings.Repeat("d", 40)}); err == nil || !strings.Contains(err.Error(), "source SHA mismatch") {
		t.Fatalf("expected-source error = %v", err)
	}
}

func TestBuildCampaignBundleRejectsInvalidFrozenInputs(t *testing.T) {
	root := writeCampaignBundleFixture(t, 4, 3)
	for _, test := range []struct {
		name         string
		sourceSHA    string
		blocImage    string
		mempoolImage string
		want         string
	}{
		{name: "short source", sourceSHA: "abc", blocImage: testCampaignBlocImage, mempoolImage: testCampaignMempoolImage, want: "source SHA"},
		{name: "mutable tag", sourceSHA: testCampaignSourceSHA, blocImage: "bloc-node:latest", mempoolImage: testCampaignMempoolImage, want: "ECR digest"},
		{name: "non ECR", sourceSHA: testCampaignSourceSHA, blocImage: "ghcr.io/example/bloc-node@sha256:" + strings.Repeat("a", 64), mempoolImage: testCampaignMempoolImage, want: "ECR digest"},
		{name: "uppercase digest", sourceSHA: testCampaignSourceSHA, blocImage: "123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:" + strings.Repeat("A", 64), mempoolImage: testCampaignMempoolImage, want: "ECR digest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildCampaignBundleManifest(root, test.sourceSHA, test.blocImage, test.mempoolImage)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("build error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadCampaignBundleRejectsMutations(t *testing.T) {
	fixture := writeCampaignBundleFixture(t, 4, 3)
	for _, test := range []struct {
		name   string
		mutate func(string, *campaignBundleManifest)
		want   string
	}{
		{name: "source", mutate: func(_ string, manifest *campaignBundleManifest) { manifest.SourceSHA = "invalid" }, want: "source SHA"},
		{name: "image", mutate: func(_ string, manifest *campaignBundleManifest) { manifest.BlocImage = "bloc-node:latest" }, want: "ECR digest"},
		{name: "corpus public id", mutate: func(_ string, manifest *campaignBundleManifest) { manifest.PublicConfigID = "other" }, want: "public config"},
		{name: "plaintext prefix", mutate: func(_ string, manifest *campaignBundleManifest) { manifest.PlaintextPrefixSetIDs["32"] = "other" }, want: "plaintext prefix"},
		{name: "encrypted prefix", mutate: func(_ string, manifest *campaignBundleManifest) { manifest.EncryptedPrefixSetIDs["128"] = "other" }, want: "encrypted prefix"},
		{name: "index assignment", mutate: func(_ string, manifest *campaignBundleManifest) { manifest.IndexAssignment = "slot-bound" }, want: "index assignment"},
		{name: "corpus bytes", mutate: func(root string, _ *campaignBundleManifest) {
			_ = os.WriteFile(filepath.Join(root, campaignBundleCorpusFile), []byte("{}"), 0644)
		}, want: "file hash"},
		{name: "crs bytes", mutate: func(root string, _ *campaignBundleManifest) {
			_ = os.WriteFile(filepath.Join(root, campaignBundleCRSFile), []byte("changed"), 0644)
		}, want: "file hash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := copyCampaignBundleFixture(t, fixture)
			manifest, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(root, &manifest)
			if err := writeJSONFileAtomic(filepath.Join(root, campaignBundleManifestFile), manifest, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadCampaignBundle(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildCampaignBundleRejectsBadSecretAndEscapingSymlink(t *testing.T) {
	fixture := writeCampaignBundleFixture(t, 4, 3)
	t.Run("share", func(t *testing.T) {
		root := copyCampaignBundleFixture(t, fixture)
		secretPath := filepath.Join(root, campaignBundleSecretDir, "operator-0.json")
		data, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatal(err)
		}
		var secret map[string]any
		if err := json.Unmarshal(data, &secret); err != nil {
			t.Fatal(err)
		}
		secret["bte_share_scalar_hex"] = strings.Repeat("0", 64)
		data, _ = json.Marshal(secret)
		if err := os.WriteFile(secretPath, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage); err == nil {
			t.Fatal("bad campaign share was accepted")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root := copyCampaignBundleFixture(t, fixture)
		outside := filepath.Join(t.TempDir(), "outside.crs")
		if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, campaignBundleCRSFile)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, campaignBundleCRSFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink error = %v", err)
		}
	})
}

func writeCampaignBundleFixture(t *testing.T, n, threshold int, bmaxOverride ...int) string {
	t.Helper()
	bmax := 128
	if len(bmaxOverride) > 0 {
		bmax = bmaxOverride[0]
	}
	root := t.TempDir()
	identityPath := filepath.Join(root, campaignBundleIdentityFile)
	crsPath := filepath.Join(root, campaignBundleCRSFile)
	secretDir := filepath.Join(root, campaignBundleSecretDir)
	identity, crs, secrets, err := buildCampaignIdentity(campaignIdentityOptions{
		ClusterID:   "final-n" + string(rune('0'+n)),
		IdentityOut: identityPath,
		CRSOut:      crsPath,
		SecretsDir:  secretDir,
		N:           n,
		Threshold:   threshold,
		BMax:        bmax,
		Limits:      defaultResourceLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(crsPath, crs, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomic(identityPath, identity, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secretDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if err := writeJSONFileAtomic(filepath.Join(secretDir, "operator-"+string(rune('0'+secret.OperatorID))+".json"), secret, 0600); err != nil {
			t.Fatal(err)
		}
	}
	suite := newSuite()
	publicKey, err := unmarshalPointHex(suite, identity.PublicKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	publicID, err := be.PublicConfigID(identity.BMax, identity.CRSSHA256, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	prefixes := map[string]string{"8": "prefix-8", "32": "prefix-32", "128": "prefix-128"}
	encryptedPrefixes := map[string]string{"8": "encrypted-8", "32": "encrypted-32", "128": "encrypted-128"}
	if bmax == 512 {
		prefixes["512"] = "prefix-512"
		encryptedPrefixes["512"] = "encrypted-512"
	}
	corpus := map[string]any{
		"schema_version": "bloc-encrypted-corpus-v1", "ciphertext_wire_version": be.LibraryVersion,
		"public_config_id": publicID, "plaintext_master_corpus_id": "plaintext-master",
		"plaintext_prefix_set_ids": prefixes, "encrypted_corpus_id": "encrypted-master",
		"encrypted_prefix_set_ids": encryptedPrefixes,
		"bmax":                     bmax, "available_count": bmax, "index_assignment": "coordinated-position-v1",
		"ordered_index_schedule": []int{}, "class_counts": map[string]any{}, "candidates": []any{},
	}
	if err := writeJSONFileAtomic(filepath.Join(root, campaignBundleCorpusFile), corpus, 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyCampaignBundleFixture(t *testing.T, source string) string {
	t.Helper()
	destination := t.TempDir()
	for _, name := range []string{campaignBundleIdentityFile, campaignBundleCRSFile, campaignBundleCorpusFile} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	secretDestination := filepath.Join(destination, campaignBundleSecretDir)
	if err := os.Mkdir(secretDestination, 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(source, campaignBundleSecretDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(source, campaignBundleSecretDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(secretDestination, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

func setCampaignBundleCombineWorkers(t *testing.T, root string, workers int) {
	t.Helper()
	path := filepath.Join(root, campaignBundleIdentityFile)
	identity, _, err := readCampaignIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	identity.Limits.MaxCombineWorkers = workers
	if err := writeJSONFileAtomic(path, identity, 0644); err != nil {
		t.Fatal(err)
	}
}

func writeCampaignBundleManifestForTest(t *testing.T, root string, manifest campaignBundleManifest, includeWorkers bool) {
	t.Helper()
	path := filepath.Join(root, campaignBundleManifestFile)
	data := campaignBundleManifestBytesForTest(t, manifest, includeWorkers)
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

func campaignBundleManifestBytesForTest(t *testing.T, manifest campaignBundleManifest, includeWorkers bool) []byte {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if includeWorkers {
		return data
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "max_combine_workers")
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
