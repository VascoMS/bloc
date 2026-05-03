package mempool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

type RPCClient struct {
	endpoint   string
	httpClient *http.Client
}

func NewRPCClient(endpoint string, httpClient *http.Client) *RPCClient {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &RPCClient{endpoint: endpoint, httpClient: httpClient}
}

func (c *RPCClient) Fetch(ctx context.Context) ([]Transaction, error) {
	return c.TxpoolContent(ctx)
}

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type txpoolContent map[string]map[string]map[string]rpcTx

type rpcTx struct {
	Hash              string `json:"hash"`
	From              string `json:"from"`
	To                string `json:"to"`
	Nonce             string `json:"nonce"`
	Gas               string `json:"gas"`
	Input             string `json:"input"`
	GasPrice          string `json:"gasPrice"`
	MaxFeePerGas      string `json:"maxFeePerGas"`
	MaxPriorityFeeGas string `json:"maxPriorityFeePerGas"`
}

func (c *RPCClient) TxpoolContent(ctx context.Context) ([]Transaction, error) {
	payload, err := json.Marshal(rpcReq{
		JSONRPC: "2.0",
		Method:  "txpool_content",
		Params:  []any{},
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

	var content txpoolContent
	if err := json.Unmarshal(out.Result, &content); err != nil {
		return nil, err
	}
	return flatten(content)
}

func flatten(content txpoolContent) ([]Transaction, error) {
	result := make([]Transaction, 0)
	sections := []struct {
		name   string
		queued bool
	}{{name: "pending", queued: false}, {name: "queued", queued: true}}

	for _, sec := range sections {
		bySender := content[sec.name]
		for _, byNonce := range bySender {
			for _, raw := range byNonce {
				tx, err := normalizeTx(raw, sec.queued)
				if err != nil {
					return nil, err
				}
				ClassifyAndParse(&tx)
				result = append(result, tx)
			}
		}
	}
	return result, nil
}

func normalizeTx(raw rpcTx, queued bool) (Transaction, error) {
	nonce, err := parseHexUint64(raw.Nonce)
	if err != nil {
		return Transaction{}, fmt.Errorf("invalid nonce for %s: %w", raw.Hash, err)
	}
	gas, err := parseHexUint64(raw.Gas)
	if err != nil {
		return Transaction{}, fmt.Errorf("invalid gas for %s: %w", raw.Hash, err)
	}

	return Transaction{
		Hash:              strings.ToLower(raw.Hash),
		From:              strings.ToLower(raw.From),
		To:                strings.ToLower(raw.To),
		Nonce:             nonce,
		Gas:               gas,
		Input:             strings.ToLower(raw.Input),
		GasPriceWei:       parseHexBig(raw.GasPrice),
		MaxFeePerGasWei:   parseHexBig(raw.MaxFeePerGas),
		MaxPriorityFeeWei: parseHexBig(raw.MaxPriorityFeeGas),
		IsQueued:          queued,
	}, nil
}

func parseHexUint64(v string) (uint64, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "0x" {
		return 0, nil
	}
	if strings.HasPrefix(v, "0x") {
		return strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 64)
	}
	return strconv.ParseUint(v, 10, 64)
}

func parseHexBig(v string) *big.Int {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "0x" {
		return big.NewInt(0)
	}
	n := new(big.Int)
	if strings.HasPrefix(v, "0x") {
		n.SetString(strings.TrimPrefix(v, "0x"), 16)
		return n
	}
	n.SetString(v, 10)
	return n
}
