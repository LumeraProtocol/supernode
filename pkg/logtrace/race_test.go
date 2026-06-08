//go:build race

package logtrace

import (
	"context"
	"sync"
	"testing"
)

func TestSetupConcurrentWithLoggingRaceFree(t *testing.T) {
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%10 == 0 {
				Setup("race-test")
			}
			Debug(ctx, "debug", Fields{"i": i})
			Info(ctx, "info", Fields{"i": i})
			Warn(ctx, "warn", Fields{"i": i})
		}(i)
	}
	wg.Wait()
}
