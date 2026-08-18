package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriptedChatGatewayServesCompletionsAndHeaders(t *testing.T) {
	gateway := StartScriptedGateway(func(callIndex int, prompt string) ScriptedReply {
		require.Equal(t, 0, callIndex)
		require.Equal(t, "Who won?", prompt)
		return ScriptedReply{
			ServeMode:   "exact_cache",
			Content:     "France",
			RequestID:   "req-1",
			ReceiptHash: "hash-1",
			ReceiptURL:  "https://example.test/r/1",
		}
	})
	t.Cleanup(gateway.Close)

	payload, err := json.Marshal(map[string]any{
		"model":    "e2e-model",
		"messages": []map[string]string{{"role": "user", "content": "Who won?"}},
	})
	require.NoError(t, err)

	resp, err := gateway.Client().Post(gateway.URL+"/v1/chat/completions", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "exact_cache", resp.Header.Get("x-nextmodel-serve-mode"))
	require.Equal(t, "req-1", resp.Header.Get("x-nextmodel-request-id"))
	require.Equal(t, "hash-1", resp.Header.Get("x-nextmodel-receipt-hash"))
	require.Equal(t, "https://example.test/r/1", resp.Header.Get("x-nextmodel-receipt-url"))

	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "France", body.Choices[0].Message.Content)
	require.Equal(t, 1, gateway.Calls())
}

func TestScriptedChatGatewayPlansByCallIndex(t *testing.T) {
	gateway := StartScriptedGateway(func(callIndex int, prompt string) ScriptedReply {
		if callIndex == 0 {
			return ScriptedReply{ServeMode: "fresh", Content: "first " + prompt}
		}
		return ScriptedReply{ServeMode: "exact_cache", Content: "second " + prompt}
	})
	t.Cleanup(gateway.Close)

	post := func(text string) string {
		payload, err := json.Marshal(map[string]any{
			"model":    "e2e-model",
			"messages": []map[string]string{{"role": "user", "content": text}},
		})
		require.NoError(t, err)
		resp, err := gateway.Client().Post(gateway.URL+"/v1/chat/completions", "application/json", bytes.NewReader(payload))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		return resp.Header.Get("x-nextmodel-serve-mode")
	}

	require.Equal(t, "fresh", post("same prompt"))
	require.Equal(t, "exact_cache", post("same prompt"))
	require.Equal(t, 2, gateway.Calls())
}
