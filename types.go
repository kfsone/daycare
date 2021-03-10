package daycare

// registration is a request to register a new entity, providing the key
// and the value assigned to it, and an interface to respond via if the
// key already exists.
type registration struct {
	key      string
	value    interface{}
	response chan<- interface{}
}

// lookup is a blocking query for the value of a specific key. If the
// key is not yet registered, will block until the
type lookup struct {
	key      string
	response chan<- interface{}
}
