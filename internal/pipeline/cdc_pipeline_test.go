package pipeline

import (
	"testing"
	"time"

	"acid/internal/schema"
)

func TestPipelineStartAndStop(t *testing.T) {
	pipeline := NewPipeline(nil, nil, schema.NewSchemaRegistry(nil))

	done := make(chan struct{})
	go func() {
		pipeline.Start()
		pipeline.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline did not stop cleanly")
	}
}
