package mempool

import (
	"context"
	"log"
	"time"
)

type Source interface {
	Fetch(ctx context.Context) ([]Transaction, error)
}

type Reader struct {
	source   Source
	store    *Store
	interval time.Duration
}

func NewReader(source Source, store *Store, interval time.Duration) *Reader {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Reader{source: source, store: store, interval: interval}
}

func (r *Reader) PollOnce(ctx context.Context) error {
	txs, err := r.source.Fetch(ctx)
	if err != nil {
		return err
	}
	r.store.ReplaceAll(txs)
	return nil
}

func (r *Reader) Run(ctx context.Context) {
	if err := r.PollOnce(ctx); err != nil {
		log.Printf("initial poll failed: %v", err)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.PollOnce(ctx); err != nil {
				log.Printf("mempool poll failed: %v", err)
			}
		}
	}
}
