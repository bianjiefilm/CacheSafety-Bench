package cache

import (
	"net/http"
	"strings"
)

const (
	HeaderServeMode   = "x-nextmodel-serve-mode"
	HeaderRequestID   = "x-nextmodel-request-id"
	HeaderReceiptHash = "x-nextmodel-receipt-hash"
	HeaderReceiptURL  = "x-nextmodel-receipt-url"
)

const (
	ServeModeExactCache     = "exact_cache"
	ServeModeCanonicalCache = "canonical_cache"
	ServeModeLNBeta         = "ln_beta"
)

const LayerLNBeta Layer = "ln_beta"

type Observation struct {
	ServeMode   string `json:"serve_mode,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	ReceiptHash string `json:"receipt_hash,omitempty"`
	ReceiptURL  string `json:"receipt_url,omitempty"`
}

func ParseGatewayHeaders(headers http.Header) Observation {
	if headers == nil {
		return Observation{}
	}
	return Observation{
		ServeMode:   strings.TrimSpace(headers.Get(HeaderServeMode)),
		RequestID:   strings.TrimSpace(headers.Get(HeaderRequestID)),
		ReceiptHash: strings.TrimSpace(headers.Get(HeaderReceiptHash)),
		ReceiptURL:  strings.TrimSpace(headers.Get(HeaderReceiptURL)),
	}
}

func LayerFromServeMode(mode string) Layer {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ServeModeExactCache:
		return LayerExact
	case ServeModeCanonicalCache:
		return LayerCanonical
	case ServeModeLNBeta:
		return LayerLNBeta
	default:
		return LayerMiss
	}
}

func IsObservedCacheHit(mode string) bool {
	return LayerFromServeMode(mode) != LayerMiss
}
