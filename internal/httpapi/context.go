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
