package db

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// A failed ping is the error ConnectWithRetry logs and gives up with; it once wrapped the
// nil pool error instead and printed %!w(<nil>). No database: the context is already done.
func TestConnectWithRetryReportsThePingError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var log bytes.Buffer
	_, err := ConnectWithRetry(ctx, "postgres://nobody@127.0.0.1:1/none?sslmode=disable", slog.New(slog.NewTextHandler(&log, nil)))
	if err == nil {
		t.Fatal("connected to nothing")
	}
	if got := log.String(); strings.Contains(got, "%!w") || !strings.Contains(got, "ping failed: context canceled") {
		t.Errorf("logged %q, want the ping's own error", got)
	}
}
