package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type ScriptedReply struct {
	ServeMode   string
	Content     string
	RequestID   string
	ReceiptHash string
	ReceiptURL  string
}

type ScriptedPlan func(callIndex int, prompt string) ScriptedReply

type ScriptedGateway struct {
	*httptest.Server
	plan  ScriptedPlan
	mu    sync.Mutex
	calls int
}

func StartScriptedGateway(plan ScriptedPlan) *ScriptedGateway {
	if plan == nil {
		plan = func(int, string) ScriptedReply {
			return ScriptedReply{ServeMode: "fresh", Content: "ok"}
		}
	}
	gateway := &ScriptedGateway{plan: plan}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", gateway.handleChatCompletions)
	mux.HandleFunc("/chat/completions", gateway.handleChatCompletions)
	gateway.Server = httptest.NewServer(mux)
	return gateway
}

func (g *ScriptedGateway) Calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func (g *ScriptedGateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	prompt := promptFromChatBody(body)

	g.mu.Lock()
	index := g.calls
	g.calls++
	g.mu.Unlock()

	reply := g.plan(index, prompt)
	if reply.ServeMode != "" {
		w.Header().Set(cachepkg.HeaderServeMode, reply.ServeMode)
	}
	if reply.RequestID != "" {
		w.Header().Set(cachepkg.HeaderRequestID, reply.RequestID)
	}
	if reply.ReceiptHash != "" {
		w.Header().Set(cachepkg.HeaderReceiptHash, reply.ReceiptHash)
	}
	if reply.ReceiptURL != "" {
		w.Header().Set(cachepkg.HeaderReceiptURL, reply.ReceiptURL)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":    "chatcmpl-e2e",
		"model": "e2e-model",
		"choices": []map[string]any{{
			"message":       map[string]string{"role": "assistant", "content": reply.Content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     8,
			"completion_tokens": 4,
			"total_tokens":      12,
		},
	})
}

func promptFromChatBody(body []byte) string {
	var payload struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for i := len(payload.Messages) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(payload.Messages[i].Content); text != "" {
			return text
		}
	}
	return ""
}
