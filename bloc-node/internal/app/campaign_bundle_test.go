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

func writeCampaignBundleFixture(t *testing.T, n, threshold int) string {
	t.Helper()
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
		BMax:        128,
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
	corpus := map[string]any{
		"schema_version": "bloc-encrypted-corpus-v1", "ciphertext_wire_version": be.LibraryVersion,
		"public_config_id": publicID, "plaintext_master_corpus_id": "plaintext-master",
		"plaintext_prefix_set_ids": prefixes, "encrypted_corpus_id": "encrypted-master",
		"encrypted_prefix_set_ids": map[string]string{"8": "encrypted-8", "32": "encrypted-32", "128": "encrypted-128"},
		"bmax":                     128, "available_count": 128, "index_assignment": "coordinated-position-v1",
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
