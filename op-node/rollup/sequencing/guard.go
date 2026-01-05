package sequencing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// GuardConfig configures the optional guard client.
type GuardConfig struct {
	URL      string
	Timeout  time.Duration
	FailOpen bool
}

type GuardClient interface {
	CheckBlock(ctx context.Context, payload *eth.ExecutionPayloadEnvelope, ref eth.L2BlockRef) (GuardDecision, error)
}

type GuardDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

type guardRequest struct {
	Block         guardBlock     `json:"block"`
	TxCount       int            `json:"txCount"`
	GasUsed       uint64         `json:"gasUsed"`
	SafeHead      *guardBlockRef `json:"safeHead,omitempty"`
	FinalizedHead *guardBlockRef `json:"finalizedHead,omitempty"`
	L1Origin      guardL1Origin  `json:"l1Origin"`
	Extra         map[string]any `json:"extra,omitempty"`
}

type guardBlock struct {
	Number     uint64      `json:"number"`
	Hash       common.Hash `json:"hash"`
	ParentHash common.Hash `json:"parentHash"`
}

type guardBlockRef struct {
	Number uint64      `json:"number"`
	Hash   common.Hash `json:"hash"`
}

type guardL1Origin struct {
	Number uint64      `json:"number"`
	Hash   common.Hash `json:"hash"`
}

type httpGuardClient struct {
	url    string
	client *http.Client
	log    log.Logger
}

func NewHTTPGuardClient(cfg GuardConfig, logger log.Logger) GuardClient {
	if cfg.URL == "" {
		return nil
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &httpGuardClient{
		url: cfg.URL,
		client: &http.Client{
			Timeout: timeout,
		},
		log: logger,
	}
}

func (g *httpGuardClient) CheckBlock(ctx context.Context, payload *eth.ExecutionPayloadEnvelope, ref eth.L2BlockRef) (GuardDecision, error) {
	reqBody := guardRequest{
		Block: guardBlock{
			Number:     ref.Number,
			Hash:       payload.ExecutionPayload.BlockHash,
			ParentHash: payload.ExecutionPayload.ParentHash,
		},
		TxCount: len(payload.ExecutionPayload.Transactions),
		GasUsed: uint64(payload.ExecutionPayload.GasUsed),
		L1Origin: guardL1Origin{
			Number: ref.L1Origin.Number,
			Hash:   ref.L1Origin.Hash,
		},
		SafeHead: &guardBlockRef{
			Number: ref.Number,
			Hash:   ref.Hash,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return GuardDecision{}, fmt.Errorf("marshal guard request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, bytes.NewReader(body))
	if err != nil {
		return GuardDecision{}, fmt.Errorf("build guard request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return GuardDecision{}, fmt.Errorf("guard request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return GuardDecision{}, fmt.Errorf("guard returned %d", resp.StatusCode)
	}

	var decision GuardDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		return GuardDecision{}, fmt.Errorf("decode guard response: %w", err)
	}
	if !decision.Allow && decision.Reason == "" {
		decision.Reason = "guard denied block"
	}
	return decision, nil
}
