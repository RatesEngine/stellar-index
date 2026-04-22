package middleware

import (
	"context"
	"net/http"
)

// ctxKey is the concrete type for this package's context keys.
// Keeping it unexported prevents cross-package collisions (two
// packages writing the same string key would otherwise clobber).
type ctxKey string

const (
	ctxKeyRequestID ctxKey = "request_id"
	ctxKeyRemoteIP  ctxKey = "remote_ip"
)

// RequestIDFrom reads the request ID from r's context, or "" if
// RequestID middleware wasn't applied.
func RequestIDFrom(r *http.Request) string {
	return stringFromCtx(r.Context(), ctxKeyRequestID)
}

// RemoteIPFrom reads the resolved remote IP (X-Forwarded-For or
// r.RemoteAddr), or "" if Logger middleware wasn't applied.
func RemoteIPFrom(r *http.Request) string {
	return stringFromCtx(r.Context(), ctxKeyRemoteIP)
}

func stringFromCtx(ctx context.Context, k ctxKey) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(k).(string)
	return v
}

func withString(ctx context.Context, k ctxKey, v string) context.Context {
	return context.WithValue(ctx, k, v)
}
