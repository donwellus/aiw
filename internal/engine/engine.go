package engine

import (
	"context"
	"time"
)

type Request struct {
	Stage     string
	Prompt    string
	Workdir   string
	Model     string
	SessionID string
	Timeout   time.Duration
	Env       []string
}

type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		CostUSD:      u.CostUSD + other.CostUSD,
	}
}

type Result struct {
	Output    string
	SessionID string
	Usage     Usage
	Raw       []byte
	Command   string
	Duration  time.Duration
}

type Engine interface {
	Name() string
	Run(ctx context.Context, req Request) (Result, error)
}
