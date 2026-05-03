package mempool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AlchemyPendingClient struct {
	endpoint   string
	httpClient *http.Client
	ttl        time.Duration
	now        func() time.Time

	mu            sync.Mutex
	filterID      string
	byHash        map[string]trackedPendingTx
	bySenderNonce map[string]string
}

type trackedPendingTx struct {
	tx        Transaction
	updatedAt time.Time
}

func NewAlchemyPendingClient(endpoint string, httpClient *http.Client, ttl time.Duration) *AlchemyPendingClient {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &AlchemyPendingClient{
		endpoint:      endpoint,
		httpClient:    httpClient,
		ttl:           ttl,
		now:           time.Now,
		byHash:        map[string]trackedPendingTx{},
		bySenderNonce: map[string]string{},
	}
}

func (c *AlchemyPendingClient) Fetch(ctx context.Context) ([]Transaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureFilter(ctx); err != nil {
		return nil, err
	}

	hashes, err := c.getFilterChanges(ctx)
	if err != nil {
		if isFilterNotFound(err) {
			c.filterID = ""
			if err := c.ensureFilter(ctx); err != nil {
				return nil, err
			}
			hashes, err = c.getFilterChanges(ctx)
		}
		if err != nil {
			return nil, err
		}
	}

	now := c.now()
	for _, hash := range hashes {
		hash = strings.ToLower(strings.TrimSpace(hash))
		if hash == "" || hash == "0x" {
			continue
		}
		raw, ok, err := c.getTransactionByHash(ctx, hash)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}

		tx, err := normalizeTx(raw, false)
		if err != nil {
			continue
		}
		ClassifyAndParse(&tx)
		c.upsert(tx, now)
	}

	c.expire(now)

	out := make([]Transaction, 0, len(c.byHash))
	for _, tracked := range c.byHash {
		out = append(out, tracked.tx)
	}
	return out, nil
}

func (c *AlchemyPendingClient) ensureFilter(ctx context.Context) error {
	if c.filterID != "" {
		return nil
	}
	var id string
	if err := c.rpcCall(ctx, "eth_newPendingTransactionFilter", []any{}, &id); err != nil {
		return err
	}
	c.filterID = strings.ToLower(strings.TrimSpace(id))
	if c.filterID == "" || c.filterID == "0x" {
		return fmt.Errorf("empty pending tx filter id")
	}
	return nil
}

func (c *AlchemyPendingClient) getFilterChanges(ctx context.Context) ([]string, error) {
	var hashes []string
	if err := c.rpcCall(ctx, "eth_getFilterChanges", []any{c.filterID}, &hashes); err != nil {
		return nil, err
	}
	return hashes, nil
}

func (c *AlchemyPendingClient) getTransactionByHash(ctx context.Context, hash string) (rpcTx, bool, error) {
	var raw *rpcTx
	if err := c.rpcCall(ctx, "eth_getTransactionByHash", []any{hash}, &raw); err != nil {
		return rpcTx{}, false, err
	}
	if raw == nil {
		return rpcTx{}, false, nil
	}
	return *raw, true, nil
}

func (c *AlchemyPendingClient) upsert(tx Transaction, now time.Time) {
	nonceKey := tx.From + "|" + fmt.Sprintf("%d", tx.Nonce)

	if existingHash, ok := c.bySenderNonce[nonceKey]; ok {
		existing, exists := c.byHash[existingHash]
		if exists {
			cmp := tx.EffectiveFeePerGas().Cmp(existing.tx.EffectiveFeePerGas())
			if cmp < 0 || (cmp == 0 && tx.Hash > existing.tx.Hash) {
				return
			}
			delete(c.byHash, existingHash)
		}
	}

	c.byHash[tx.Hash] = trackedPendingTx{tx: tx, updatedAt: now}
	c.bySenderNonce[nonceKey] = tx.Hash
}

func (c *AlchemyPendingClient) expire(now time.Time) {
	for hash, tracked := range c.byHash {
		if now.Sub(tracked.updatedAt) <= c.ttl {
			continue
		}
		delete(c.byHash, hash)
		nonceKey := tracked.tx.From + "|" + fmt.Sprintf("%d", tracked.tx.Nonce)
		if current, ok := c.bySenderNonce[nonceKey]; ok && current == hash {
			delete(c.bySenderNonce, nonceKey)
		}
	}
}

func (c *AlchemyPendingClient) rpcCall(ctx context.Context, method string, params []any, out any) error {
	payload, err := json.Marshal(rpcReq{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("rpc status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rpcOut rpcResp
	if err := json.Unmarshal(body, &rpcOut); err != nil {
		return err
	}
	if rpcOut.Error != nil {
		return fmt.Errorf("rpc error (%d): %s", rpcOut.Error.Code, rpcOut.Error.Message)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(rpcOut.Result, out); err != nil {
		return err
	}
	return nil
}

func isFilterNotFound(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "filter not found")
}
