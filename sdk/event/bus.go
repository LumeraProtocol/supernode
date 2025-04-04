package event

import (
	"action/log"
	"context"
	"runtime/debug"
	"sync"
)

// Handler is a function that processes events
type Handler func(Event)

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
func NewBus(logger log.Logger, maxWorkers int) *Bus {
	// Use default logger if nil is provided
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	// Use default worker count if invalid value is provided
	if maxWorkers <= 0 {
		maxWorkers = 50
	}

	return &Bus{
		subscribers: make(map[EventType][]Handler),
		logger:      logger,
		workerPool:  make(chan struct{}, maxWorkers),
		maxWorkers:  maxWorkers,
	}
}

// Subscribe registers a handler for a specific event type
func (b *Bus) Subscribe(eventType EventType, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ctx := context.Background()
	b.logger.Debug(ctx, "Subscribing handler to event type", "eventType", eventType)

	if _, exists := b.subscribers[eventType]; !exists {
		b.subscribers[eventType] = []Handler{}
	}
	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
}

// SubscribeAll registers a handler for all event types
func (b *Bus) SubscribeAll(handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ctx := context.Background()
	b.logger.Debug(ctx, "Subscribing handler to all event types")
	b.wildcardHandlers = append(b.wildcardHandlers, handler)
}

// safelyCallHandler executes a handler with proper panic recovery
func (b *Bus) safelyCallHandler(handler Handler, event Event) {
	// Acquire a worker slot
	b.workerPool <- struct{}{}

	go func() {
		defer func() {
			// Release worker slot when done
			<-b.workerPool

			// Recover from panics
			if r := recover(); r != nil {
				stackTrace := debug.Stack()
				ctx := context.Background()
				b.logger.Error(ctx,
					"Event handler panicked",
					"error", r,
					"eventType", event.Type,
					"stackTrace", string(stackTrace),
				)
			}
		}()

		// Create a deep copy of the event to avoid race conditions
		eventCopy := copyEvent(event)

		// Execute the handler
		handler(eventCopy)
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
		Data:      make(map[string]interface{}, len(e.Data)),
	}

	// Copy the data map
	for k, v := range e.Data {
		copied.Data[k] = v
	}

	return copied
}

// Publish sends an event to all relevant subscribers
func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ctx := context.Background()
	b.logger.Debug(ctx, "Publishing event",
		"type", event.Type,
		"taskID", event.TaskID,
		"taskType", event.TaskType)

	// Call type-specific handlers
	if handlers, exists := b.subscribers[event.Type]; exists {
		b.logger.Debug(ctx, "Calling type-specific handlers",
			"eventType", event.Type,
			"handlerCount", len(handlers))

		for _, handler := range handlers {
			b.safelyCallHandler(handler, event)
		}
	}

	// Call wildcard handlers
	if len(b.wildcardHandlers) > 0 {
		b.logger.Debug(ctx, "Calling wildcard handlers",
			"handlerCount", len(b.wildcardHandlers))

		for _, handler := range b.wildcardHandlers {
			b.safelyCallHandler(handler, event)
		}
	}
}

// WaitForHandlers waits for all event handlers to complete
func (b *Bus) WaitForHandlers() {
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
func (b *Bus) Close() {
	b.WaitForHandlers()
}
