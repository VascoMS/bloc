package mempool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Store struct {
	mu   sync.RWMutex
	byID map[string]Transaction
}

func NewStore() *Store {
	return &Store{byID: map[string]Transaction{}}
}

func (s *Store) ReplaceAll(txs []Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[string]Transaction, len(txs))
	for _, tx := range txs {
		next[tx.Hash] = tx
	}
	s.byID = next
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	arr := make([]Transaction, 0, len(s.byID))
	for _, tx := range s.byID {
		arr = append(arr, tx)
	}
	s.mu.RUnlock()

	sort.Slice(arr, func(i, j int) bool {
		if arr[i].From != arr[j].From {
			return arr[i].From < arr[j].From
		}
		if arr[i].Nonce != arr[j].Nonce {
			return arr[i].Nonce < arr[j].Nonce
		}
		return arr[i].Hash < arr[j].Hash
	})

	lines := make([]string, 0, len(arr))
	for _, tx := range arr {
		line := fmt.Sprintf("%s|%s|%d|%d|%s|%s|%t", tx.Hash, tx.From, tx.Nonce, tx.Gas, tx.Kind, tx.EffectiveFeePerGas().String(), tx.IsQueued)
		lines = append(lines, line)
	}
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))

	return Snapshot{
		Transactions: arr,
		Count:        len(arr),
		Hash:         "0x" + hex.EncodeToString(h[:]),
	}
}

type Snapshot struct {
	Transactions []Transaction `json:"transactions"`
	Count        int           `json:"count"`
	Hash         string        `json:"hash"`
}
