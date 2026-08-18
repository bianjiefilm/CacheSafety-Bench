package cache

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGatewayHeadersReadsServeModeAndReceipts(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-nextmodel-serve-mode", "exact_cache")
	headers.Set("X-Nextmodel-Request-Id", "req-123")
	headers.Set("x-nextmodel-receipt-hash", "hash-abc")
	headers.Set("x-nextmodel-receipt-url", "https://example.test/receipts/abc")

	got := ParseGatewayHeaders(headers)

	require.Equal(t, Observation{
		ServeMode:   "exact_cache",
		RequestID:   "req-123",
		ReceiptHash: "hash-abc",
		ReceiptURL:  "https://example.test/receipts/abc",
	}, got)
}

func TestParseGatewayHeadersTrimsAndIgnoresMissing(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-nextmodel-serve-mode", "  ln_beta  ")

	got := ParseGatewayHeaders(headers)

	require.Equal(t, Observation{ServeMode: "ln_beta"}, got)
}

func TestLayerFromServeModeMapsKnownHitsAndTreatsOthersAsMiss(t *testing.T) {
	cases := []struct {
		mode  string
		layer Layer
		hit   bool
	}{
		{mode: "exact_cache", layer: LayerExact, hit: true},
		{mode: "EXACT_CACHE", layer: LayerExact, hit: true},
		{mode: "canonical_cache", layer: LayerCanonical, hit: true},
		{mode: "ln_beta", layer: LayerLNBeta, hit: true},
		{mode: "fresh", layer: LayerMiss, hit: false},
		{mode: "unknown", layer: LayerMiss, hit: false},
		{mode: "", layer: LayerMiss, hit: false},
		{mode: "  ", layer: LayerMiss, hit: false},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			require.Equal(t, tc.layer, LayerFromServeMode(tc.mode))
			require.Equal(t, tc.hit, IsObservedCacheHit(tc.mode))
		})
	}
}
