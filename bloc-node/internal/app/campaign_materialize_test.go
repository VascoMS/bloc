package app

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMaterializeCampaignConfigPreservesFrozenInputsAcrossTopologies(t *testing.T) {
	root := writeCampaignBundleFixture(t, 4, 3)
	manifest, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomic(filepath.Join(root, campaignBundleManifestFile), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	bundle, err := loadCampaignBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	sameInventory := campaignTestInventory("same-az")
	threeInventory := campaignTestInventory("three-region")
	base := campaignMaterializeOptions{
		ClusterOut: "generated/cluster.json", CRSOut: "generated/cluster.crs",
		Topology: "T0-same-az", MempoolURL: "http://mempool-il:8080",
		HTTPPort: 8000, P2PPort: 9000, HTTPHostMode: "private-ip", P2PHostMode: "private-ip",
	}
	sameCluster, sameCRS, sameRemote, err := buildMaterializedCampaignConfigs(bundle, sameInventory, base)
	if err != nil {
		t.Fatalf("same-AZ materialization: %v", err)
	}
	base.Topology = "T2-three-region"
	threeCluster, threeCRS, threeRemote, err := buildMaterializedCampaignConfigs(bundle, threeInventory, base)
	if err != nil {
		t.Fatalf("three-region materialization: %v", err)
	}
	if sameCluster.PublicKeyHex != threeCluster.PublicKeyHex || sameCluster.CRSSHA256 != threeCluster.CRSSHA256 || !reflect.DeepEqual(sameCRS, threeCRS) {
		t.Fatal("materialization changed frozen BTE public inputs")
	}
	if !reflect.DeepEqual(sameCluster.Provider, threeCluster.Provider) || !reflect.DeepEqual(sameRemote.Corpus, threeRemote.Corpus) {
		t.Fatal("materialization changed frozen corpus inputs")
	}
	if !reflect.DeepEqual(sameCluster.Blockspace, threeCluster.Blockspace) || !reflect.DeepEqual(sameCluster.Limits, threeCluster.Limits) {
		t.Fatal("materialization changed frozen limits")
	}
	for i := range sameCluster.Nodes {
		if sameCluster.Nodes[i].P2PPeerID != threeCluster.Nodes[i].P2PPeerID {
			t.Fatalf("operator %d peer identity changed", i)
		}
		if sameCluster.Nodes[i].HTTPAdvertiseURL == threeCluster.Nodes[i].HTTPAdvertiseURL {
			t.Fatalf("operator %d topology address did not change", i)
		}
	}
}

func TestMaterializeCampaignConfigEnablesACSTraceOnlyWhenRequested(t *testing.T) {
	root := writeCampaignBundleFixture(t, 4, 3)
	manifest, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomic(filepath.Join(root, campaignBundleManifestFile), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	bundle, err := loadCampaignBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	options := campaignMaterializeOptions{
		ClusterOut: "cluster.json", CRSOut: "cluster.crs", Topology: "T0-same-az",
		MempoolURL: "http://mempool-il:8080", HTTPPort: 8000, P2PPort: 9000,
		HTTPHostMode: "private-ip", P2PHostMode: "private-ip",
		StreamMode: streamModePersistentLanes,
	}
	legacy, _, legacyRemote, err := buildMaterializedCampaignConfigs(bundle, campaignTestInventory("same-az"), options)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Diagnostics.ACSTrace {
		t.Fatal("legacy campaign unexpectedly enabled ACS tracing")
	}
	if legacy.Network.StreamMode != streamModePersistentLanes || legacyRemote.StreamMode != streamModePersistentLanes {
		t.Fatalf("stream mode not retained: cluster=%q remote=%q", legacy.Network.StreamMode, legacyRemote.StreamMode)
	}
	options.ACSTrace = true
	diagnostic, _, _, err := buildMaterializedCampaignConfigs(bundle, campaignTestInventory("same-az"), options)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnostic.Diagnostics.ACSTrace {
		t.Fatal("diagnostic campaign did not enable ACS tracing")
	}
}

func TestMaterializeCampaignConfigRejectsInvalidPlacement(t *testing.T) {
	root := writeCampaignBundleFixture(t, 4, 3)
	manifest, err := buildCampaignBundleManifest(root, testCampaignSourceSHA, testCampaignBlocImage, testCampaignMempoolImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomic(filepath.Join(root, campaignBundleManifestFile), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	bundle, err := loadCampaignBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	base := campaignMaterializeOptions{
		ClusterOut: "cluster.json", CRSOut: "cluster.crs", Topology: "T0-same-az",
		MempoolURL: "http://mempool-il:8080", HTTPPort: 8000, P2PPort: 9000,
		HTTPHostMode: "private-ip", P2PHostMode: "private-ip",
	}
	for _, test := range []struct {
		name   string
		mutate func(*ec2Inventory)
		want   string
	}{
		{name: "duplicate id", mutate: func(value *ec2Inventory) { value.Nodes[1].ID = 0 }, want: "duplicate"},
		{name: "wrong count", mutate: func(value *ec2Inventory) { value.Nodes = value.Nodes[:3] }, want: "node count"},
		{name: "wrong instance", mutate: func(value *ec2Inventory) { value.Nodes[0].InstanceType = "t3.micro" }, want: "t3.small"},
		{name: "wrong same az", mutate: func(value *ec2Inventory) { value.Nodes[0].Zone = "us-east-1b" }, want: "us-east-1a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := campaignTestInventory("same-az")
			test.mutate(&inventory)
			if _, _, _, err := buildMaterializedCampaignConfigs(bundle, inventory, base); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("placement error = %v, want %q", err, test.want)
			}
		})
	}
	base.Topology = "T2-three-region"
	wrongModulo := campaignTestInventory("three-region")
	wrongModulo.Nodes[1].Region = "eu-central-1"
	if _, _, _, err := buildMaterializedCampaignConfigs(bundle, wrongModulo, base); err == nil || !strings.Contains(err.Error(), "modulo-three") {
		t.Fatalf("modulo placement error = %v", err)
	}
}

func campaignTestInventory(topology string) ec2Inventory {
	regions := []string{"us-east-1", "eu-west-1", "eu-central-1"}
	nodes := make([]ec2InventoryHost, 4)
	for i := range nodes {
		region, zone, octet := "us-east-1", "us-east-1a", 10+i
		if topology == "three-region" {
			region = regions[i%3]
			zone = region + "a"
			octet = 20 + i
		}
		nodes[i] = ec2InventoryHost{ID: i, Label: "operator", PrivateIP: fmt.Sprintf("10.0.0.%d", octet), Region: region, Zone: zone, InstanceType: "t3.small"}
	}
	return ec2Inventory{
		Deployment: map[string]string{"environment": "ec2"},
		Controller: &ec2InventoryHost{ID: -1, PrivateIP: "10.0.0.9", Region: "us-east-1", Zone: "us-east-1a", InstanceType: "t3.small"},
		Nodes:      nodes,
	}
}
