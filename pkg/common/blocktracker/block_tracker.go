package blocktracker

import (
	"context"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
)

const (
	defaultRetries                     = 3
	defaultDelayDurationBetweenRetries = 5 * time.Second
	defaultRPCConnectTimeout           = 15 * time.Second
	// Update duration in case last update was success
	defaultSuccessUpdateDuration = 10 * time.Second
	// Update duration in case last update was failed - prevent too much call to Lumera
	defaultFailedUpdateDuration = 5 * time.Second
	defaultNextBlockTimeout     = 30 * time.Minute
)

// LumeraClient defines interface functions BlockCntTracker expects from Lumera
type LumeraClient interface {
	// GetBlockCount returns block height of blockchain
	GetBlockCount(ctx context.Context) (int32, error)
}

// BlockCntTracker defines a block tracker - that will keep current block height
type BlockCntTracker struct {
	mtx                 sync.Mutex
	LumeraClient        LumeraClient
	curBlockCnt         int32
	lastSuccess         time.Time
	lastRetried         time.Time
	lastErr             error
	delayBetweenRetries time.Duration
	retries             int
}

// New returns an instance of BlockCntTracker
func New(LumeraClient LumeraClient) *BlockCntTracker {
	return &BlockCntTracker{
		LumeraClient:        LumeraClient,
		curBlockCnt:         0,
		delayBetweenRetries: defaultDelayDurationBetweenRetries,
		retries:             defaultRetries,
	}
}

func (tracker *BlockCntTracker) refreshBlockCount(retries int) {
	tracker.lastRetried = time.Now().UTC()
	for i := 0; i < retries; i = i + 1 {
		ctx, cancel := context.WithTimeout(context.Background(), defaultRPCConnectTimeout)
		blockCnt, err := tracker.LumeraClient.GetBlockCount(ctx)
		if err == nil {
			tracker.curBlockCnt = blockCnt
			tracker.lastSuccess = time.Now().UTC()
			cancel()
			tracker.lastErr = nil
			return
		}
		cancel()

		tracker.lastErr = err
		// delay between retries
		time.Sleep(tracker.delayBetweenRetries)
	}

}

// GetBlockCount return current block count
// it will get from cache if last refresh is small than defaultSuccessUpdateDuration
// or will refresh it by call from Lumera daemon to get the latest one if defaultSuccessUpdateDuration expired
func (tracker *BlockCntTracker) GetBlockCount() (int32, error) {
	tracker.mtx.Lock()
	defer tracker.mtx.Unlock()

	shouldRefresh := false

	if tracker.lastSuccess.After(tracker.lastRetried) {
		if time.Now().UTC().After(tracker.lastSuccess.Add(defaultSuccessUpdateDuration)) {
			shouldRefresh = true
		}
	} else {
		// prevent update too much
		if time.Now().UTC().After(tracker.lastRetried.Add(defaultFailedUpdateDuration)) {
			shouldRefresh = true
		}
	}

	if shouldRefresh {
		tracker.refreshBlockCount(tracker.retries)
	}

	if tracker.curBlockCnt == 0 {
		return 0, errors.Errorf("failed to get blockcount: %w", tracker.lastErr)
	}

	return tracker.curBlockCnt, nil
}

// WaitTillNextBlock will wait until next block height is greater than blockCnt
func (tracker *BlockCntTracker) WaitTillNextBlock(ctx context.Context, blockCnt int32) error {
	for {
		select {
		case <-ctx.Done():
			return errors.Errorf("context done: %w", ctx.Err())
		case <-time.After(defaultNextBlockTimeout):
			return errors.Errorf("timeout waiting for next block")
		case <-time.After(defaultSuccessUpdateDuration):
			curBlockCnt, err := tracker.GetBlockCount()
			if err != nil {
				return errors.Errorf("failed to get blockcount: %w", err)
			}

			if curBlockCnt > blockCnt {
				return nil
			}
		}
	}
}
