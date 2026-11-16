package redisutil

import (
	"context"
	"fmt"
)

// BoundedByContext runs call and stops WAITING for it when ctx ends, answering
// the context's error, whatever the client does.
//
// The bound cannot be left to the Redis client: NewClientLazy's comes from
// redis.ParseURL with its defaults, so a context's deadline does not reach the
// socket (ContextTimeoutEnabled is off) and a read waits for ReadTimeout. So the
// call runs on a goroutine of its own and the caller waits for whichever comes
// first. The call given up on is not cancelled: it ends when the client's own
// timeout ends it, and its answer goes into a buffered channel nobody reads. A
// panic in it is recovered into an error, since nothing is waiting on it.
func BoundedByContext[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("the Redis call panicked: %v", r)}
			}
		}()
		value, err := call(ctx)
		done <- outcome{value: value, err: err}
	}()
	select {
	case o := <-done:
		return o.value, o.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("the Redis call did not answer in time: %w", ctx.Err())
	}
}
