package cache

import (
	"context"
	"math"
	"strings"
	"sync"

	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"
)

type Layer string

const (
	LayerExact     Layer = "exact"
	LayerCanonical Layer = "canonical"
	LayerSemantic  Layer = "semantic"
	LayerMiss      Layer = "miss"
)

type UpstreamFunc func(context.Context, Request) (Response, error)

type SimilarityFunc func(a Request, b Request) float64
type SemanticCandidateFunc func(context.Context, Request) (SemanticCandidate, bool, error)

type SemanticCandidate struct {
	Request    Request
	Response   Response
	Similarity float64
	TopK       []SemanticTopKCandidate
}

type SemanticTopKCandidate struct {
	Rank                   int     `json:"rank"`
	Similarity             float64 `json:"similarity"`
	CandidatePromptPreview string  `json:"candidate_prompt_preview,omitempty"`
	CandidateAnswerPreview string  `json:"candidate_answer_preview,omitempty"`
	AnswerLength           int     `json:"answer_length,omitempty"`
	TeacherScore           float64 `json:"teacher_score,omitempty"`
	CreatedAt              string  `json:"created_at,omitempty"`
	CandidateID            string  `json:"candidate_id,omitempty"`
}

type LookupResult struct {
	Layer           Layer                   `json:"layer"`
	Response        Response                `json:"response"`
	Similarity      float64                 `json:"similarity,omitempty"`
	CachedPrompt    string                  `json:"cached_prompt,omitempty"`
	CandidateAnswer string                  `json:"candidate_answer,omitempty"`
	SemanticTopK    []SemanticTopKCandidate `json:"semantic_topk,omitempty"`
	RefusedByRule   safety.Rule             `json:"refused_by_rule,omitempty"`
	RefusalDetail   string                  `json:"refusal_detail,omitempty"`
	UpstreamCalled  bool                    `json:"upstream_called"`
}

type PipelineConfig struct {
	SemanticThreshold float64
	Similarity        SimilarityFunc
	SemanticCandidate SemanticCandidateFunc
	Safety            safety.Checker
}

type Pipeline struct {
	mu         sync.RWMutex
	exact      map[string]Response
	canonical  map[string]Response
	semantic   []semanticEntry
	threshold  float64
	similarity SimilarityFunc
	candidate  SemanticCandidateFunc
	safety     *safety.Checker
}

type semanticEntry struct {
	req  Request
	resp Response
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.SemanticThreshold <= 0 {
		cfg.SemanticThreshold = 0.95
	}
	if cfg.Similarity == nil {
		cfg.Similarity = tokenJaccardSimilarity
	}
	checker := cfg.Safety
	if checker.IsZero() {
		checker = safety.DefaultChecker()
	}
	return &Pipeline{
		exact:      make(map[string]Response),
		canonical:  make(map[string]Response),
		threshold:  cfg.SemanticThreshold,
		similarity: cfg.Similarity,
		candidate:  cfg.SemanticCandidate,
		safety:     &checker,
	}
}

func (p *Pipeline) Put(req Request, resp Response) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exact[exactKey(req)] = resp
	p.canonical[canonicalKey(req)] = resp
	p.semantic = append(p.semantic, semanticEntry{req: req, resp: resp})
}

func (p *Pipeline) LookupExact(req Request) (Response, bool) {
	if p == nil {
		return Response{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	resp, ok := p.exact[exactKey(req)]
	return resp, ok
}

func (p *Pipeline) Handle(ctx context.Context, req Request, upstream UpstreamFunc) (LookupResult, error) {
	p.mu.RLock()
	if resp, ok := p.exact[exactKey(req)]; ok {
		p.mu.RUnlock()
		return LookupResult{Layer: LayerExact, Response: resp, CachedPrompt: PromptText(req)}, nil
	}
	if resp, ok := p.canonical[canonicalKey(req)]; ok {
		p.mu.RUnlock()
		return LookupResult{Layer: LayerCanonical, Response: resp, CachedPrompt: PromptText(req)}, nil
	}
	if p.candidate != nil {
		p.mu.RUnlock()
		candidate, ok, err := p.candidate(ctx, req)
		if err != nil {
			return LookupResult{}, err
		}
		if ok {
			return p.handleSemanticCandidate(ctx, req, semanticEntry{req: candidate.Request, resp: candidate.Response}, candidate.Similarity, candidate.TopK, upstream)
		}
		return callUpstream(ctx, req, upstream)
	}
	entry, similarity, ok := p.bestSemanticCandidateLocked(req)
	p.mu.RUnlock()

	if ok {
		return p.handleSemanticCandidate(ctx, req, entry, similarity, nil, upstream)
	}

	return callUpstream(ctx, req, upstream)
}

func (p *Pipeline) handleSemanticCandidate(ctx context.Context, req Request, entry semanticEntry, similarity float64, topK []SemanticTopKCandidate, upstream UpstreamFunc) (LookupResult, error) {
	decision := p.semanticSafetyDecision(req, entry.req, entry.resp)
	if decision.Allowed {
		return LookupResult{Layer: LayerSemantic, Response: entry.resp, Similarity: similarity, CachedPrompt: PromptText(entry.req), CandidateAnswer: entry.resp.Text, SemanticTopK: topK}, nil
	}
	result, err := callUpstream(ctx, req, upstream)
	result.RefusedByRule = decision.Rule
	result.RefusalDetail = decision.Detail
	result.Similarity = similarity
	result.CachedPrompt = PromptText(entry.req)
	result.CandidateAnswer = entry.resp.Text
	result.SemanticTopK = topK
	return result, err
}

func (p *Pipeline) bestSemanticCandidateLocked(req Request) (semanticEntry, float64, bool) {
	var best semanticEntry
	bestScore := math.Inf(-1)
	for _, entry := range p.semantic {
		score := p.similarity(req, entry.req)
		if score > bestScore {
			best = entry
			bestScore = score
		}
	}
	if bestScore >= p.threshold {
		return best, bestScore, true
	}
	return semanticEntry{}, bestScore, false
}

func (p *Pipeline) semanticSafetyDecision(req Request, cachedReq Request, resp Response) safety.Decision {
	checker := safety.DefaultChecker()
	if p.safety != nil {
		checker = *p.safety
	}
	return checker.Evaluate(safety.Input{
		Prompt:             PromptText(req),
		CachedPrompt:       PromptText(cachedReq),
		HasTools:           len(req.Tools) > 0,
		HasFunctionCall:    hasFunctionCall(req),
		CachedTeacherScore: resp.TeacherScore,
		HitCount:           resp.HitCount,
		LastRefreshed:      resp.LastRefreshed,
	})
}

func hasFunctionCall(req Request) bool {
	if len(req.Extra) == 0 {
		return false
	}
	for _, key := range []string{"function_call", "tool_choice"} {
		if value, ok := req.Extra[key]; ok && value != nil {
			return true
		}
	}
	return false
}

func callUpstream(ctx context.Context, req Request, upstream UpstreamFunc) (LookupResult, error) {
	resp, err := upstream(ctx, req)
	if err != nil {
		return LookupResult{Layer: LayerMiss, UpstreamCalled: true}, err
	}
	return LookupResult{Layer: LayerMiss, Response: resp, UpstreamCalled: true}, nil
}

func tokenJaccardSimilarity(a Request, b Request) float64 {
	left := tokenSet(PromptText(a))
	right := tokenSet(PromptText(b))
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	intersection := 0
	for token := range left {
		if right[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenSet(value string) map[string]bool {
	fields := strings.Fields(strings.ToLower(value))
	out := make(map[string]bool, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, " \t\n\r.,!?;:\"'()[]{}")
		if token != "" {
			out[token] = true
		}
	}
	return out
}
