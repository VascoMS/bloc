package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mempool-il/internal/api"
	"mempool-il/internal/inclusion"
	"mempool-il/internal/mempool"
)

func main() {
	var (
		rpcURL       = flag.String("rpc-url", "http://127.0.0.1:8545", "execution/public JSON-RPC URL")
		sourceType   = flag.String("source", "txpool", "mempool source: txpool | public-pending | alchemy-pending | replay-placeholder")
		alchemyTTL   = flag.Duration("alchemy-ttl", 5*time.Minute, "retention for alchemy pending tx cache")
		corpusPath   = flag.String("corpus", "", "JSONL corpus of raw signed Ethereum target transactions for replay-placeholder")
		clusterPath  = flag.String("cluster-config", "", "BLOC cluster config for replay-placeholder encryption")
		replaySlot   = flag.Uint64("replay-slot", 1, "slot id used when encrypting replay-placeholder payloads")
		listenAddr   = flag.String("listen", ":8080", "HTTP listen address")
		pollInterval = flag.Duration("poll-interval", 2*time.Second, "mempool polling interval")
		maxTxs       = flag.Int("max-items", 128, "max inclusion list tx count")
		maxGas       = flag.Uint64("max-gas", 0, "max inclusion list total gas (0 means auto)")
		maxBlockGas  = flag.Uint64("max-block-gas", 30_000_000, "max block gas used for auto inclusion list cap")
	)
	flag.Parse()

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store := mempool.NewStore()
	httpClient := &http.Client{Timeout: 10 * time.Second}

	var source mempool.Source
	var slotSource mempool.SlotSource
	switch *sourceType {
	case "txpool":
		source = mempool.NewRPCClient(*rpcURL, httpClient)
	case "public-pending":
		source = mempool.NewPublicRPCClient(*rpcURL, httpClient)
	case "alchemy-pending":
		source = mempool.NewAlchemyPendingClient(*rpcURL, httpClient, *alchemyTTL)
	case "replay-placeholder":
		var err error
		replaySource, err := mempool.NewReplayPlaceholderClient(mempool.ReplayPlaceholderConfig{
			CorpusPath:  *corpusPath,
			ClusterPath: *clusterPath,
			Slot:        *replaySlot,
		})
		if err != nil {
			log.Fatalf("load replay-placeholder source: %v", err)
		}
		source = replaySource
		slotSource = replaySource
	default:
		log.Fatalf("invalid -source %q; expected txpool, public-pending, alchemy-pending or replay-placeholder", *sourceType)
	}

	reader := mempool.NewReader(source, store, *pollInterval)
	effectiveMaxGas := *maxGas
	if effectiveMaxGas == 0 {
		effectiveMaxGas = *maxBlockGas * 2
	}
	builder := inclusion.NewBuilder(inclusion.Config{MaxTransactions: *maxTxs, MaxGas: effectiveMaxGas})
	apiServer := api.NewServer(store, builder)
	if slotSource != nil {
		apiServer = api.NewServerWithSlotSource(store, builder, slotSource)
	}

	if slotSource == nil {
		go reader.Run(rootCtx)
	} else if txs, err := source.Fetch(rootCtx); err == nil {
		store.ReplaceAll(txs)
	}

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-rootCtx.Done()
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = httpServer.Shutdown(ctx)
	}()

	log.Printf("service listening on %s", *listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}
