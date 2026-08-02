package app

import (
	"fmt"
	"log"
	"os"
)

// Run dispatches the bloc-node CLI. It also registers gob protocol types before
// any command starts because the ACS payload adapter uses gob for deterministic
// candidate and block-body encoding.
func Run(args []string) {
	registerGobTypes()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(args) < 2 {
		usage()
		os.Exit(2)
	}
	switch args[1] {
	case "gen-config":
		if err := genConfig(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "gen-ec2-config":
		if err := genEC2Config(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "gen-campaign-identity":
		if err := genCampaignIdentity(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "verify-campaign-bundle":
		if err := verifyCampaignBundle(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "materialize-campaign-config":
		if err := materializeCampaignConfig(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "bind-encrypted-corpus":
		if err := bindEncryptedCorpus(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "run":
		if err := runNode(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "submit":
		if err := submitTx(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "eval-local":
		if err := evalLocal(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "eval-suite":
		if err := evalSuite(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "eval-remote":
		if err := evalRemote(args[2:]); err != nil {
			log.Fatal(err)
		}
	case "report":
		if err := reportDemo(args[2:]); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  bloc-node gen-config --nodes 4 --threshold 3 --bmax 128 --out cluster.json
  bloc-node gen-ec2-config --inventory deploy/ec2/inventory.json --cluster-out cluster.ec2.json --remote-eval-out remote-eval.ec2.json
  bloc-node gen-campaign-identity --cluster-id final-n4 --nodes 4 --threshold 3 --bmax 128 --identity-out cluster-identity.json --crs-out cluster.crs --secrets-dir secrets
  bloc-node verify-campaign-bundle --bundle-root bundle-n4 --source-sha SHA --bloc-image ECR@DIGEST --mempool-image ECR@DIGEST --write-manifest
  bloc-node materialize-campaign-config --bundle-root bundle-n4 --inventory inventory.json --topology T0-same-az --cluster-out cluster.json --crs-out cluster.crs --remote-eval-out remote-eval.json
  bloc-node bind-encrypted-corpus --config cluster.json --corpus encrypted-corpus.json --mempool-url http://mempool-il:8080
  bloc-node run --config cluster.json --secrets secrets/operator-0.json --id 0 --slot 1 --start-after 3s
  bloc-node submit --url http://127.0.0.1:8000 --tx 0x010203
  bloc-node eval-local --nodes 4 --batch-sizes 8,32 --tx-size 256 --out-dir results
  bloc-node eval-suite --profile m1-baseline --experiment-id m1-baseline --out-dir results/m1-local/baseline-persistent
  bloc-node eval-remote --config remote-eval.json --batch-size 8 --repetitions 3 --out-dir results/distributed/smoke
  bloc-node report --dir results/mvp-demo/latest --out results/mvp-demo/latest/DEMO_REPORT.md
`)
}
