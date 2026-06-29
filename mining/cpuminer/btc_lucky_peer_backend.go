package cpuminer

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/peer"
)

const (
	BTCLuckySubCmdGetJob       = "btclucky.getjob"
	BTCLuckySubCmdJob          = "btclucky.job"
	BTCLuckySubCmdSubmit       = "btclucky.submit"
	BTCLuckySubCmdSubmitResult = "btclucky.submitresult"
)

type PeerJobResponse struct {
	Job   *CompactMiningJob `json:"job,omitempty"`
	Error string            `json:"error,omitempty"`
}

type PeerSubmitResponse struct {
	Found *FoundBlockRecord `json:"found,omitempty"`
	Error string            `json:"error,omitempty"`
}

type PeerTemplateBackend struct {
	mu       sync.Mutex
	network  string
	timeout  time.Duration
	getPeer  func() *peer.Peer
	found    []FoundBlockRecord
	lastJob  time.Time
	lastErr  string
	lastPeer string
}

func NewPeerTemplateBackend(network string, timeout time.Duration, getPeer func() *peer.Peer) *PeerTemplateBackend {
	if network == "" {
		network = "mainnet"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &PeerTemplateBackend{
		network: network,
		timeout: timeout,
		getPeer: getPeer,
	}
}

func (b *PeerTemplateBackend) Name() string {
	return BTCLuckyBackendPeerTemplate
}

func (b *PeerTemplateBackend) Start() error {
	if b.getPeer == nil {
		return fmt.Errorf("btc lucky peer-template backend has no peer resolver")
	}
	return nil
}

func (b *PeerTemplateBackend) Stop() {}

func (b *PeerTemplateBackend) IsReady() bool {
	return b.getPeer != nil && b.getPeer() != nil
}

func (b *PeerTemplateBackend) CurrentJob(req JobRequest) (*CompactMiningJob, error) {
	if req.Network == "" {
		req.Network = b.network
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	p := b.getPeer()
	if p == nil || !p.Connected() {
		err := fmt.Errorf("btc lucky peer-template backend has no connected template peer")
		b.setError(err, "")
		return nil, err
	}
	msg, err := p.SendMineBlockRequestAndWait(b.timeout, BTCLuckySubCmdGetJob, payload, nil, BTCLuckySubCmdJob)
	if err != nil {
		b.setError(err, p.String())
		return nil, err
	}
	var resp PeerJobResponse
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		b.setError(err, p.String())
		return nil, err
	}
	if resp.Error != "" {
		err := fmt.Errorf("%s", resp.Error)
		b.setError(err, p.String())
		return nil, err
	}
	if resp.Job == nil {
		err := fmt.Errorf("btc lucky template peer returned empty job")
		b.setError(err, p.String())
		return nil, err
	}
	b.mu.Lock()
	b.lastJob = time.Now()
	b.lastErr = ""
	b.lastPeer = p.String()
	b.mu.Unlock()
	return resp.Job, nil
}

func (b *PeerTemplateBackend) SubmitSolution(solution *MiningSolution) (*FoundBlockRecord, error) {
	payload, err := json.Marshal(solution)
	if err != nil {
		return nil, err
	}
	p := b.getPeer()
	if p == nil || !p.Connected() {
		err := fmt.Errorf("btc lucky peer-template backend has no connected template peer")
		b.setError(err, "")
		return nil, err
	}
	msg, err := p.SendMineBlockRequestAndWait(b.timeout, BTCLuckySubCmdSubmit, payload, nil, BTCLuckySubCmdSubmitResult)
	if err != nil {
		b.setError(err, p.String())
		return nil, err
	}
	var resp PeerSubmitResponse
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		b.setError(err, p.String())
		return nil, err
	}
	if resp.Error != "" {
		err := fmt.Errorf("%s", resp.Error)
		b.setError(err, p.String())
		if resp.Found != nil {
			b.rememberFound(*resp.Found)
			return resp.Found, err
		}
		return nil, err
	}
	if resp.Found == nil {
		err := fmt.Errorf("btc lucky template peer returned empty submit result")
		b.setError(err, p.String())
		return nil, err
	}
	b.rememberFound(*resp.Found)
	return resp.Found, nil
}

func (b *PeerTemplateBackend) Status() TemplateServiceStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return TemplateServiceStatus{
		Enabled:          true,
		Running:          true,
		Backend:          BTCLuckyBackendPeerTemplate,
		BTCNetwork:       b.network,
		LastTemplateTime: b.lastJob,
		LastError:        b.lastErr,
		LastSubmitResult: b.lastPeer,
	}
}

func (b *PeerTemplateBackend) FoundBlocks() []FoundBlockRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]FoundBlockRecord, len(b.found))
	copy(out, b.found)
	return out
}

func (b *PeerTemplateBackend) setError(err error, peer string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.lastErr = err.Error()
	}
	b.lastPeer = peer
}

func (b *PeerTemplateBackend) rememberFound(record FoundBlockRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastErr = ""
	b.found = append(b.found, record)
	if len(b.found) > 32 {
		b.found = b.found[len(b.found)-32:]
	}
}
