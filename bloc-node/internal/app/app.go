package app

import (
	"fmt"
	"log"
	"os"
)

// Run dispatches the bloc-node CLI. It also registers gob protocol types before
// any command starts because both the compatibility TCP transport and current
// ACS payload adapter depend on gob's concrete type registry.
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
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  bloc-node gen-config --nodes 4 --threshold 3 --bmax 128 --out cluster.json
  bloc-node run --config cluster.json --id 0 --slot 1 --start-after 3s
  bloc-node submit --url http://127.0.0.1:8000 --tx 0x010203
  bloc-node eval-local --nodes 4 --batch-sizes 8,32 --tx-size 256 --out-dir results
`)
}
