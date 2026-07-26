package main

import (
	"flag"
	"io"
	"log"
	"os"

	"mempool-il/internal/mempool"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("corpus-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	corpusPath := flags.String("corpus", "", "labelled JSONL corpus of signed Ethereum target transactions")
	clusterPath := flags.String("cluster-config", "", "BLOC public cluster config used for encryption")
	outputPath := flags.String("out", "results/client-overhead/client_overhead.csv", "output CSV path")
	slot := flags.Uint64("slot", 1, "slot bound into BTE ciphertexts")
	samplesPerClass := flags.Int("samples-per-class", 100, "raw measurements per transaction class")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return mempool.WriteClientOverheadReport(mempool.ClientOverheadConfig{
		CorpusPath:      *corpusPath,
		ClusterPath:     *clusterPath,
		OutputPath:      *outputPath,
		Slot:            *slot,
		SamplesPerClass: *samplesPerClass,
	})
}
