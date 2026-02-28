package proposer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum-optimism/optimism/op-proposer/proposer/source"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
)

// GuardClient checks whether a proposal should proceed.
type GuardClient interface {
	CheckProposal(ctx context.Context, proposal source.Proposal) (GuardDecision, error)
}

// GuardDecision is the expected response shape from the guard.
type GuardDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

type guardRequest struct {
	Root           common.Hash    `json:"root"`
	SequenceNumber uint64         `json:"sequenceNumber"`
	ExtraData      hexutil.Bytes  `json:"extraData"`
	CurrentL1      guardBlockRef  `json:"currentL1"`
	SafeHead       *guardBlockRef `json:"safeHead,omitempty"`
	FinalizedHead  *guardBlockRef `json:"finalizedHead,omitempty"`
}

type guardBlockRef struct {
	Number uint64      `json:"number"`
	Hash   common.Hash `json:"hash"`
}

// HTTPGuardClient is a lightweight HTTP client that POSTs proposal data and expects a GuardDecision response.
type HTTPGuardClient struct {
	url      string
	failOpen bool
	client   *http.Client
	log      log.Logger
}

func NewHTTPGuardClient(url string, timeout time.Duration, failOpen bool, log log.Logger) GuardClient {
	if url == "" {
		return nil
	}
	return &HTTPGuardClient{
		url:      url,
		failOpen: failOpen,
		client: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}
}

func (g *HTTPGuardClient) CheckProposal(ctx context.Context, proposal source.Proposal) (GuardDecision, error) {
	payload := guardRequestFromProposal(proposal)
	body, err := json.Marshal(payload)
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
		decision.Reason = "guard rejected proposal"
	}
	return decision, nil
}

func guardRequestFromProposal(p source.Proposal) guardRequest {
	req := guardRequest{
		Root:           p.Root,
		SequenceNumber: p.SequenceNum,
		ExtraData:      hexutil.Bytes(p.ExtraData()),
		CurrentL1: guardBlockRef{
			Number: p.CurrentL1.Number,
			Hash:   p.CurrentL1.Hash,
		},
	}

	if p.Legacy.SafeL2 != (eth.L2BlockRef{}) {
		req.SafeHead = &guardBlockRef{
			Number: p.Legacy.SafeL2.Number,
			Hash:   p.Legacy.SafeL2.Hash,
		}
	}

	if p.Legacy.FinalizedL2 != (eth.L2BlockRef{}) {
		req.FinalizedHead = &guardBlockRef{
			Number: p.Legacy.FinalizedL2.Number,
			Hash:   p.Legacy.FinalizedL2.Hash,
		}
	}

	return req
}
