package daycare

import (
	"sync"
)

type unregistered struct{}

var unregisteredValue = &unregistered{}

// Registry provides a concurrency-safe key-value store with lookups that
// block until either the key exists/is registered or the registry is
// stopped.
type Registry struct {
	// incoming requests to register.
	registrations chan *registration
	// incoming queries.
	lookups   chan *lookup
	pending   map[string][]chan<- interface{}
	registry  map[string]interface{}
	waitgroup sync.WaitGroup

	// Callbacks
	onDefer	  func ()
	onResolve func (int)

	// Statistics
	Stats Stats
}

// Stats aggregates runtime statistics from the manager.
type Stats struct {
	// Hits is a count of how many Lookups were immediately resolved.
	Hits int
	// Defers is a count of how many lookups got deferred for resolution.
	Defers int
	// Resolved is a count of how many lookups were deferred and then resolved.
	Resolved int
	// Misses is a count of lookups deferred but not resolved.
	Misses int
	// Total Register calls
	Registrations int
	// Duplicate registrations
	Duplicates int
}

// NewRegistry will return an initialized DaycareCenter in an idle state, call
// Manage() to enable Register/Lookup calls, and Close() to enable access to data.
func NewRegistry() *Registry {
	return &Registry{
		registrations: make(chan *registration),
		lookups:       make(chan *lookup),
		pending:       make(map[string][]chan<- interface{}),
		registry:      make(map[string]interface{}),
		Stats:         Stats{},
	}
}

// valueToPending sends a value to each of the listed channels in a goroutine.
func valueToPending(wg *sync.WaitGroup, pending []chan<- interface{}, value interface{}, handler func(int)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, waiter := range pending {
			waiter <- value
		}
		if handler != nil {
			handler(len(pending))
		}
	}()
}

func (r *Registry) closePending() {
	// Tell all the blocked queries there's no-such-value.
	var pending map[string][]chan<- interface{}
	pending, r.pending = r.pending, nil
	for key, list := range pending {
		valueToPending(&r.waitgroup, list, unregisteredValue, r.onResolve)
		r.Stats.Misses += len(list)
		delete(r.pending, key)
	}
}

// register tries to add a new key:value to the registry, and returns the
// actual value stored in the registry afterwards.
func (r *Registry) register(name string, value interface{}) interface{} {
	if prior, exists := r.registry[name]; exists {
		r.Stats.Duplicates++
		return prior
	}
	r.registry[name] = value
	if list, exists := r.pending[name]; exists {
		valueToPending(&r.waitgroup, list, value, r.onResolve)
		r.Stats.Resolved += len(list)
		delete(r.pending, name)
	}
	r.Stats.Registrations++
	return unregisteredValue
}

// lookup either sends the response channel the value of the requested
// key or, if it's not registered, puts them on the pending queue.
func (r *Registry) lookup(name string, response chan<- interface{}) {
	if value, exists := r.registry[name]; exists {
		response <- value
		r.Stats.Hits++
	} else {
		r.pending[name] = append(r.pending[name], response)
		r.Stats.Defers++
		if r.onDefer != nil {
			r.waitgroup.Add(1)
			go func () {
				defer r.waitgroup.Done()
				r.onDefer()
			} ()
		}
	}
}

// manager is the singleton instance of a registry which handles the
// registrations and lookups/deferrals.
func (r *Registry) manager() {
	defer r.waitgroup.Done()
	defer func() {
		close(r.lookups)
		r.lookups = nil
	}()

	open := true
	for open {
		select {
		case query := <-r.lookups:
			r.lookup(query.key, query.response)
		case reg, ok := <-r.registrations:
			if !ok { // channel closed, we're done
				open = false
			} else {
				reg.response <- r.register(reg.key, reg.value)
			}
		}
	}

	r.closePending()
}

// Start starts a goroutine handling Registration and Lookup calls.
func (r *Registry) Start() {
	r.waitgroup.Add(1)
	go r.manager()
}

// Stop sends a termination signal to the registry and waits for all its tasks
// to complete. You should only send this after you are sure no additional
// Register() or Lookup() calls will be made.
func (r *Registry) Stop() {
	if r.registrations == nil {
		return
	}

	// send the stop notification
	close(r.registrations)

	// wait for all tasks to finish
	r.waitgroup.Wait()

	r.registrations = nil
}

// Values returns the key-value map after the registry is Stop()d.
func (r *Registry) Values() (map[string]interface{}, error) {
	if r.pending != nil {
		return nil, ErrRunning
	}
	return r.registry, nil
}

// Register blocks while the manager attempts to register the given key:value pair.
// If the key is already registered, the previously registered value will be returned
// and ok will be false. If there are Lookup() calls waiting to resolve the key,
// they will be notified in the background.
func (r *Registry) Register(key string, value interface{}) (registered interface{}, ok bool, err error) {
	// for the manager to respond to us on.
	response := make(chan interface{})

	r.registrations <- &registration{key: key, value: value, response: response}
	registered, ok = <-response
	if !ok {
		return nil, false, ErrClosed
	}
	close(response)
	switch registered.(type) {
	case *unregistered:
		return value, true, nil
	default:
		return registered, false, nil
	}
}

// Lookup blocks until the manager can resolve the given key or is Stop()d. If
// the manager is Stop()d without the key being registered, value will be nil and
// exists will be false. If registration is closed, we don't need to block.
func (r *Registry) Lookup(key string) (value interface{}, exists bool, err error) {
	if r.registrations == nil {
		value, exists = r.registry[key]
		return value, exists, nil
	}

	// use a single-entry buffered channel so that valueToEntry doesn't get blocked
	// when it tries to wake us.
	response := make(chan interface{}, 1)

	r.lookups <- &lookup{key: key, response: response}
	value, ok := <-response
	if !ok {
		return nil, false, ErrClosed
	}
	close(response)
	switch value.(type) {
	case *unregistered:
		return nil, false, nil

	default:
		return value, true, nil
	}
}

// OnDefer allows you to specify a callback to be invoked from the manager when
// a query is deferred. The handler is executed in its own goroutine so as not
// to block the manager, but must complete for the manager to be able to Stop
// properly.
func (r *Registry) OnDefer(handler func()) {
	r.onDefer = handler
}

// OnResolve allows you to specify a callback that is executed from the
// manager whenever one or more deferred lookups are resolved. The number
// passed is the number of queries being unblocked.
func (r *Registry) OnResolve(handler func(int)) {
	r.onResolve = handler
}
