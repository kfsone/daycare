package daycare

import "sync"

type unregistered struct{}

var unregisteredValue = &unregistered{}

type Registry struct {
	// incoming requests to register.
	registrations chan *registration
	// incoming queries.
	lookups   chan *lookup
	pending   map[string][]chan<- interface{}
	registry  map[string]interface{}
	waitgroup sync.WaitGroup
}

// NewRegistry will return an initialized DaycareCenter in an idle state, call
// Manage() to enable Register/Lookup calls, and Close() to enable access to data.
func NewRegistry() *Registry {
	return &Registry{
		registrations: make(chan *registration),
		lookups:       make(chan *lookup),
		pending:       make(map[string][]chan<- interface{}),
		registry:      make(map[string]interface{}),
	}
}

// valueToPending sends a value to each of the listed channels in a goroutine.
func (r *Registry) valueToPending(pending []chan<- interface{}, value interface{}) {
	r.waitgroup.Add(1)
	go func() {
		defer r.waitgroup.Done()
		for _, waiter := range pending {
			waiter <- value
		}
	}()
}

func (r *Registry) closePending() {
	// Tell all the blocked queries there's no-such-value.
	var pending map[string][]chan<- interface{}
	pending, r.pending = r.pending, nil
	for key, list := range pending {
		r.valueToPending(list, unregisteredValue)
		delete(r.pending, key)
	}
}

// register tries to add a new key:value to the registry, and returns the
// actual value stored in the registry afterwards.
func (r *Registry) register(name string, value interface{}) interface{} {
	if prior, exists := r.registry[name]; exists {
		return prior
	}
	r.registry[name] = value
	if pending, exists := r.pending[name]; exists {
		r.valueToPending(pending, value)
		delete(r.pending, name)
	}
	return unregisteredValue
}

// lookup either sends the response channel the value of the requested
// key or, if it's not registered, puts them on the pending queue.
func (r *Registry) lookup(name string, response chan<- interface{}) {
	if value, exists := r.registry[name]; exists {
		response <- value
	} else {
		r.pending[name] = append(r.pending[name], response)
	}
}

// Start starts a goroutine handling Registration and Lookup calls.
func (r *Registry) Start() {
	r.waitgroup.Add(1)
	go r.manager()
}

// manager is the singleton instance of a registry which handles the
// registrations and lookups/deferrals.
func (r *Registry) manager() {
	defer r.waitgroup.Done()
	defer close(r.lookups)

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

// Stop sends a termination signal to the registry and waits for all its tasks
// to complete. You should only send this after you are sure no additional
// Register() or Lookup() calls will be made.
func (r *Registry) Stop() {
	// send the stop notification
	close(r.registrations)

	// wait for all tasks to finish
	r.waitgroup.Wait()
}

// GetData allows retrieval of the registered data once the manager has been
// Stop()d.
func (r *Registry) GetData() (map[string]interface{}, error) {
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
	if value == nil {
		panic("tried to register nil value")
	}
	// for the manager to respond to us on.
	response := make(chan interface{})
	defer close(response)

	r.registrations <- &registration{key: key, value: value, response: response}
	registered, ok = <-response
	if !ok {
		return nil, false, ErrRegistrationClosed
	}
	switch registered.(type) {
	case *unregistered:
		return value, true, nil
	default:
		return registered, false, nil
	}
}

// Lookup blocks until the manager can resolve the given key or is Stop()d. If
// the manager is Stop()d without the key being registered, value will be nil and
// exists will be false.
func (r *Registry) Lookup(key string) (value interface{}, exists bool, err error) {
	response := make(chan interface{})
	defer close(response)

	r.lookups <- &lookup{key: key, response: response}
	value, ok := <-response
	if !ok {
		return nil, false, ErrLookupClosed
	}
	switch value.(type) {
	case *unregistered:
		return nil, false, nil

	default:
		return value, true, nil
	}
}
