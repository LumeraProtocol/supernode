package base

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewSuperNodeTask(t *testing.T) {
	task := NewSuperNodeTask("testprefix")
	assert.NotNil(t, task)
	assert.Equal(t, "testprefix", task.LogPrefix)
}

func TestSuperNodeTask_RunHelper(t *testing.T) {
	called := false
	cleaner := func() {
		called = true
	}

	snt := NewSuperNodeTask("log")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the helper in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := snt.RunHelper(ctx, cleaner)
		assert.NoError(t, err)
	}()

	// Give the RunHelper some time to start and block on actionCh
	time.Sleep(10 * time.Millisecond)

	// Submit dummy action to allow RunAction to proceed
	done := snt.NewAction(func(ctx context.Context) error {
		return nil
	})

	<-done // wait for action to complete

	snt.CloseActionCh() // close to allow RunAction to return
	wg.Wait()           // wait for RunHelper to exit

	assert.True(t, called)
}

func TestSuperNodeTask_RunHelper_WithError(t *testing.T) {
	snt := NewSuperNodeTask("log")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)

	var runErr error
	go func() {
		defer wg.Done()
		runErr = snt.RunHelper(ctx, func() {})
	}()

	// Give RunHelper time to start
	time.Sleep(10 * time.Millisecond)

	done := snt.NewAction(func(ctx context.Context) error {
		return fmt.Errorf("fail")
	})

	<-done              // wait for the action to complete
	snt.CloseActionCh() // allow RunAction to exit
	wg.Wait()           // wait for RunHelper to return

	assert.EqualError(t, runErr, "fail")
}
