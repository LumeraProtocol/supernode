package supernodeservice

import "context"

// internal context key to toggle P2P metrics in status requests
type ctxKey string

const ctxKeyIncludeP2P ctxKey = "include_p2p_metrics"

// WithIncludeP2PMetrics returns a child context that requests detailed P2P metrics
// (and peer info) in status responses.
func WithIncludeP2PMetrics(ctx context.Context) context.Context {
    return context.WithValue(ctx, ctxKeyIncludeP2P, true)
}

// WithP2PMetrics allows explicitly setting the include flag.
func WithP2PMetrics(ctx context.Context, include bool) context.Context {
    return context.WithValue(ctx, ctxKeyIncludeP2P, include)
}

// includeP2PMetrics reads the flag from context; defaults to false when unset.
func includeP2PMetrics(ctx context.Context) bool {
    v := ctx.Value(ctxKeyIncludeP2P)
    if b, ok := v.(bool); ok {
        return b
    }
    return false
}

