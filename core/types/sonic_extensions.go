package types

// SetSonicPayload stores an arbitrary value under the given key in the
// transaction's extra payload map. The map is backed by a sync.Map, making
// concurrent reads and writes safe without external synchronization.
func SetSonicPayload(tx *Transaction, key string, v any) {
	tx.extraPayload.Store(key, v)
}

// GetSonicPayload retrieves a typed value from the transaction's extra payload
// map. It returns the value and true if the key exists and the stored value is
// assignable to type T. If the key is missing or the value cannot be asserted
// to T, it returns the zero value of T and false.
func GetSonicPayload[T any](tx *Transaction, key string) (T, bool) {
	var zero T
	v, ok := tx.extraPayload.Load(key)
	if !ok {
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}
