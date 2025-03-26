package event

import (
	"sync"
)

// Handler is a function that processes events
type Handler func(Event)

// Bus manages event subscriptions and dispatching
type Bus struct {
	subscribers      map[EventType][]Handler // Type-specific handlers
	wildcardHandlers []Handler               // Handlers for all events
	mu               sync.RWMutex            // Protects concurrent access
}

// NewBus creates a new event bus
func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[EventType][]Handler),
	}
}

// Subscribe registers a handler for a specific event type
func (b *Bus) Subscribe(eventType EventType, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.subscribers[eventType]; !exists {
		b.subscribers[eventType] = []Handler{}
	}
	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
}

// SubscribeAll registers a handler for all event types
func (b *Bus) SubscribeAll(handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.wildcardHandlers = append(b.wildcardHandlers, handler)
}

// Publish sends an event to all relevant subscribers
func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Call type-specific handlers
	if handlers, exists := b.subscribers[event.Type]; exists {
		for _, handler := range handlers {
			go handler(event) // Run handlers asynchronously
		}
	}

	// Call wildcard handlers
	for _, handler := range b.wildcardHandlers {
		go handler(event) // Run handlers asynchronously
	}
}
