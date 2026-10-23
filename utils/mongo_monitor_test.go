package utils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSlowMongoMonitor(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	prev := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(prev)

	m := NewSlowMongoMonitor(200 * time.Millisecond)
	start := func(id int64) {
		m.Started(context.Background(), &event.CommandStartedEvent{
			CommandName: "find", RequestID: id,
			Command: bson.Raw(mustDoc(t, bson.D{{Key: "find", Value: "users"}, {Key: "filter", Value: bson.D{{Key: "email", Value: "secret@x.y"}}}})),
		})
	}
	done := func(id int64, d time.Duration) {
		m.Succeeded(context.Background(), &event.CommandSucceededEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", RequestID: id, Duration: d},
		})
	}

	start(1)
	done(1, 50*time.Millisecond)
	assert.Equal(t, 0, logs.Len(), "fast command is not logged")

	start(2)
	done(2, 450*time.Millisecond)
	require.Equal(t, 1, logs.Len())
	e := logs.All()[0]
	assert.Equal(t, "slow mongo command", e.Message)
	assert.Equal(t, "find", e.ContextMap()["command"])
	assert.Equal(t, "users", e.ContextMap()["collection"])
	assert.EqualValues(t, 450, e.ContextMap()["duration_ms"])
	assert.NotContains(t, e.ContextMap(), "filter")
	assert.NotContains(t, e.Message, "secret")
}

func mustDoc(t *testing.T, d bson.D) []byte {
	b, err := bson.Marshal(d)
	require.NoError(t, err)
	return b
}
