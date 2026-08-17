package httpapi

import (
	"context"
	"net/http"
	"time"
)

// contextWithTimeout bounds a single handler's work without outliving the request
// itself: if the client has already gone away, the derived context is cancelled too.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// contextLike is the read surface store.InstanceKeys needs, so the same call works
// from a request and from a detached background goroutine.
type contextLike = context.Context

// contextWithBackgroundTimeout bounds work that has outlived its request. A push
// round trip is started from a handler but must not be cancelled when that handler
// returns, or the notification is dropped the moment the response is written.
func contextWithBackgroundTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
