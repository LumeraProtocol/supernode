//go:generate mockery --name=State

package state

import (
	"context"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/pkg/types"
)

// State represents a state of the task.
type State interface {
	// Status returns the current status.
	Status() *Status

	// SetStatusNotifyFunc sets a function to be called after the state is updated.
	SetStatusNotifyFunc(fn func(status *Status))

	// RequiredStatus returns an error if the current status doen't match the given one.
	RequiredStatus(subStatus SubStatus) error

	// StatusHistory returns all history from the very beginning.
	StatusHistory() []*Status

	// UpdateStatus updates the status of the state by creating a new status with the given `status`.
	UpdateStatus(subStatus SubStatus)

	// SubscribeStatus returns a new subscription of the state.
	SubscribeStatus() func() <-chan *Status

	//SetStateLog set the wallet node task status log to the state status log
	SetStateLog(statusLog types.Fields)

	//InitialiseHistoryDB sets the connection to historyDB
	InitialiseHistoryDB(store queries.LocalStoreInterface)
}

type state struct {
	status  *Status
	history []*Status

	notifyFn func(status *Status)
	sync.RWMutex
	subsCh         []chan *Status
	taskID         string
	statusLog      types.Fields
	historyDBStore queries.LocalStoreInterface
}

// Status implements State.Status()
func (state *state) Status() *Status {
	return state.status
}

// SetStatusNotifyFunc implements State.SetStatusNotifyFunc()
func (state *state) SetStatusNotifyFunc(fn func(status *Status)) {
	state.notifyFn = fn
}

// RequiredStatus implements State.RequiredStatus()
func (state *state) RequiredStatus(subStatus SubStatus) error {
	if state.status.Is(subStatus) {
		return nil
	}
	return errors.Errorf("required status %q, current %q", subStatus, state.status)
}

// StatusHistory implements State.StatusHistory()
func (state *state) StatusHistory() []*Status {
	state.RLock()
	defer state.RUnlock()

	return append(state.history, state.status)
}

// UpdateStatus implements State.UpdateStatus()
func (state *state) UpdateStatus(subStatus SubStatus) {
	state.Lock()
	defer state.Unlock()

	status := NewStatus(subStatus)
	state.history = append(state.history, state.status)
	state.status = status

	history := types.TaskHistory{CreatedAt: time.Now().UTC(), TaskID: state.taskID, Status: status.String()}
	if state.statusLog.IsValid() {
		history.Details = types.NewDetails(status.String(), state.statusLog)
	}

	if state.historyDBStore != nil {
		if _, err := state.historyDBStore.InsertTaskHistory(history); err != nil {
			logtrace.Error(context.Background(), "unable to store task status", logtrace.Fields{logtrace.FieldError: err.Error()})
		}
	} else {
		store, err := queries.OpenHistoryDB()
		if err != nil {
			logtrace.Error(context.Background(), "error opening history db", logtrace.Fields{logtrace.FieldError: err.Error()})
		}

		if store != nil {
			defer store.CloseHistoryDB(context.Background())
			if _, err := store.InsertTaskHistory(history); err != nil {
				logtrace.Error(context.Background(), "unable to store task status", logtrace.Fields{logtrace.FieldError: err.Error()})
			}
		}
	}

	if state.notifyFn != nil {
		state.notifyFn(status)
	}

	for _, subCh := range state.subsCh {
		subCh := subCh
		go func() {
			subCh <- status
		}()
	}
}

// SubscribeStatus implements State.SubscribeStatus()
func (state *state) SubscribeStatus() func() <-chan *Status {
	state.RLock()
	defer state.RUnlock()

	subCh := make(chan *Status)
	state.subsCh = append(state.subsCh, subCh)

	for _, status := range append(state.history, state.status) {
		status := status
		go func() {
			subCh <- status
		}()
	}

	sub := func() <-chan *Status {
		return subCh
	}
	return sub
}

func (state *state) SetStateLog(statusLog types.Fields) {
	state.statusLog = statusLog
}

func (state *state) InitialiseHistoryDB(storeInterface queries.LocalStoreInterface) {
	state.historyDBStore = storeInterface
}

// New returns a new state instance.
func New(subStatus SubStatus, taskID string) State {
	store, err := queries.OpenHistoryDB()
	if err != nil {
		logtrace.Error(context.Background(), "error opening history db", logtrace.Fields{logtrace.FieldError: err.Error()})
	}

	if store != nil {
		defer store.CloseHistoryDB(context.Background())

		if _, err := store.InsertTaskHistory(types.TaskHistory{CreatedAt: time.Now().UTC(), TaskID: taskID,
			Status: subStatus.String()}); err != nil {
			logtrace.Error(context.Background(), "unable to store task status", logtrace.Fields{logtrace.FieldError: err.Error()})
		}
	}

	return &state{
		status: NewStatus(subStatus),
		taskID: taskID,
	}
}
