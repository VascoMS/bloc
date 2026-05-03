package mempool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type PublicRPCClient struct {
	endpoint   string
	httpClient *http.Client
}

func NewPublicRPCClient(endpoint string, httpClient *http.Client) *PublicRPCClient {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &PublicRPCClient{endpoint: endpoint, httpClient: httpClient}
}

type pendingBlock struct {
	Transactions []rpcTx `json:"transactions"`
}

func (c *PublicRPCClient) Fetch(ctx context.Context) ([]Transaction, error) {
	payload, err := json.Marshal(rpcReq{
		JSONRPC: "2.0",
		Method:  "eth_getBlockByNumber",
		Params:  []any{"pending", true},
		ID:      1,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rpc status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out rpcResp
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("rpc error (%d): %s", out.Error.Code, out.Error.Message)
	}

	var block *pendingBlock
	if err := json.Unmarshal(out.Result, &block); err != nil {
		return nil, err
	}
	if block == nil || len(block.Transactions) == 0 {
		return []Transaction{}, nil
	}

	result := make([]Transaction, 0, len(block.Transactions))
	for _, raw := range block.Transactions {
		tx, err := normalizeTx(raw, false)
		if err != nil {
			return nil, err
		}
		ClassifyAndParse(&tx)
		result = append(result, tx)
	}
	return result, nil
}
