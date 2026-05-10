package app

import (
	"fmt"
	"log"
	"os"
)

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
  pic-node gen-config --nodes 4 --threshold 3 --bmax 128 --out cluster.json
  pic-node run --config cluster.json --id 0 --slot 1 --start-after 3s
  pic-node submit --url http://127.0.0.1:8000 --tx 0x010203
  pic-node eval-local --nodes 4 --batch-sizes 8,32 --tx-size 256 --out-dir results
`)
}
