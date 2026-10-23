package utils

import (
	"context"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/event"
	"go.uber.org/zap"
)

// SlowMongoThreshold is the duration above which a Mongo command is logged.
const SlowMongoThreshold = 200 * time.Millisecond

// NewSlowMongoMonitor returns a command monitor that logs commands slower than threshold
// at WARN with command name, collection and duration only. Filters, values and PII are never logged.
func NewSlowMongoMonitor(threshold time.Duration) *event.CommandMonitor {
	var collections sync.Map // request id -> collection name
	logSlow := func(name, coll string, d time.Duration, failed bool) {
		if d <= threshold {
			return
		}
		zap.L().Warn("slow mongo command",
			zap.String("component", "mongo"),
			zap.String("command", name),
			zap.String("collection", coll),
			zap.Int64("duration_ms", d.Milliseconds()),
			zap.Bool("failed", failed),
		)
	}
	take := func(id int64) string {
		if v, ok := collections.LoadAndDelete(id); ok {
			return v.(string)
		}
		return ""
	}
	return &event.CommandMonitor{
		Started: func(_ context.Context, e *event.CommandStartedEvent) {
			if coll, ok := e.Command.Lookup(e.CommandName).StringValueOK(); ok {
				collections.Store(e.RequestID, coll)
			}
		},
		Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
			logSlow(e.CommandName, take(e.RequestID), e.Duration, false)
		},
		Failed: func(_ context.Context, e *event.CommandFailedEvent) {
			logSlow(e.CommandName, take(e.RequestID), e.Duration, true)
		},
	}
}
