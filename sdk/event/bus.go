package event

import (
	"context"
	"runtime/debug"
	"sync"

	"github.com/LumeraProtocol/supernode/v2/sdk/log"
)

// Handler is a function that processes events
type Handler func(ctx context.Context, e Event)

// Bus manages event subscriptions and dispatching
type Bus struct {
	subscribers      map[EventType][]Handler // Type-specific handlers
	wildcardHandlers []Handler               // Handlers for all events
	mu               sync.RWMutex            // Protects concurrent access
	logger           log.Logger              // Logger for event operations
	workerPool       chan struct{}           // Limits concurrent handler goroutines
	maxWorkers       int                     // Maximum number of concurrent workers
}

// NewBus creates a new event bus
func NewBus(ctx context.Context, logger log.Logger, maxWorkers int) *Bus {
	// Use default logger if nil is provided
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	// Use default worker count if invalid value is provided
	if maxWorkers <= 0 {
		maxWorkers = 50
	}

	logger.Debug(ctx, "Creating new event bus", "maxWorkers", maxWorkers)

	return &Bus{
		subscribers: make(map[EventType][]Handler),
		logger:      logger,
		workerPool:  make(chan struct{}, maxWorkers),
		maxWorkers:  maxWorkers,
	}
}

// Subscribe registers a handler for a specific event type
func (b *Bus) Subscribe(ctx context.Context, eventType EventType, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.logger.Debug(ctx, "Subscribing handler to event type", "eventType", eventType)

	if _, exists := b.subscribers[eventType]; !exists {
		b.subscribers[eventType] = []Handler{}
	}
	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
}

// SubscribeAll registers a handler for all event types
func (b *Bus) SubscribeAll(ctx context.Context, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.logger.Debug(ctx, "Subscribing handler to all event types")
	b.wildcardHandlers = append(b.wildcardHandlers, handler)
}

// safelyCallHandler executes a handler with proper panic recovery
func (b *Bus) safelyCallHandler(ctx context.Context, handler Handler, event Event) {
	// Acquire a worker slot
	b.workerPool <- struct{}{}

	go func() {
		defer func() {
			// Release worker slot when done
			<-b.workerPool

			// Recover from panics
			if r := recover(); r != nil {
				stackTrace := debug.Stack()
				b.logger.Error(ctx, "Event handler panicked", "error", r, "eventType", event.Type, "stackTrace", string(stackTrace))
			}
		}()

		// Create a deep copy of the event to avoid race conditions
		eventCopy := copyEvent(event)

		// Execute the handler with the provided context
		handler(ctx, eventCopy)
	}()
}

// copyEvent creates a deep copy of an event
func copyEvent(e Event) Event {
	// Copy the basic event properties
	copied := Event{
		Type:      e.Type,
		TaskID:    e.TaskID,
		TaskType:  e.TaskType,
		Timestamp: e.Timestamp,
		ActionID:  e.ActionID,
		Data:      make(EventData, len(e.Data)),
	}

	// Copy the data map
	for k, v := range e.Data {
		copied.Data[k] = v
	}

	return copied
}

// Publish sends an event to all relevant subscribers
func (b *Bus) Publish(ctx context.Context, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	b.logger.Debug(ctx, "Publishing event", "type", event.Type, "taskID", event.TaskID, "taskType", event.TaskType)

	// Call type-specific handlers
	if handlers, exists := b.subscribers[event.Type]; exists {
		b.logger.Debug(ctx, "Calling type-specific handlers", "eventType", event.Type, "handlerCount", len(handlers))

		for _, handler := range handlers {
			b.safelyCallHandler(ctx, handler, event)
		}
	}

	// Call wildcard handlers
	if len(b.wildcardHandlers) > 0 {
		b.logger.Debug(ctx, "Calling wildcard handlers", "handlerCount", len(b.wildcardHandlers))

		for _, handler := range b.wildcardHandlers {
			b.safelyCallHandler(ctx, handler, event)
		}
	}
}

// WaitForHandlers waits for all event handlers to complete
func (b *Bus) WaitForHandlers(ctx context.Context) {
	b.logger.Debug(ctx, "Waiting for all handlers to complete")

	// Fill the worker pool to capacity (blocks until all workers are free)
	for i := 0; i < b.maxWorkers; i++ {
		b.workerPool <- struct{}{}
	}

	// Release all workers
	for i := 0; i < b.maxWorkers; i++ {
		<-b.workerPool
	}
}

// Close releases resources used by the event bus
func (b *Bus) Close(ctx context.Context) {
	b.logger.Debug(ctx, "Closing event bus")
	b.WaitForHandlers(ctx)
}
