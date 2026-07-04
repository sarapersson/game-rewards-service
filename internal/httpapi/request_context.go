package httpapi

import "context"

type requestIDContextKey struct{}

func contextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func requestIDFromRequest(r interface {
	Context() context.Context
}) string {
	requestID, ok := r.Context().Value(requestIDContextKey{}).(string)
	if !ok {
		return ""
	}

	return requestID
}
