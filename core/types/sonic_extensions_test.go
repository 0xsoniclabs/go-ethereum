package types

import (
	"fmt"
	"sync"
	"testing"
)

func TestSonicPayload_IsThreadSafe(t *testing.T) {
	tx := NewTx(&LegacyTx{})
	var wg sync.WaitGroup
	const keyMods = 10
	const valueMods = 10

	wg.Add(keyMods * valueMods * 2)
	for i := range keyMods {
		key := fmt.Sprintf("key%d", i)
		for j := range valueMods {
			go func() {
				defer wg.Done()
				SetSonicPayload(tx, key, j)
			}()
			go func() {
				defer wg.Done()
				GetSonicPayload[int](tx, key)
			}()
		}
	}
	wg.Wait()
}

func TestSonicPayload_TypedReturnsFalseIfMistyped(t *testing.T) {
	tx := NewTx(&LegacyTx{})
	SetSonicPayload(tx, "num", 42)

	// Attempt to retrieve as a different type.
	val, ok := GetSonicPayload[string](tx, "num")
	if ok {
		t.Fatal("expected ok to be false when retrieving with wrong type")
	}
	if val != "" {
		t.Fatalf("expected zero value for string, got %q", val)
	}
}

func TestSonicPayload_CanStoreMultipleKeys(t *testing.T) {
	tx := NewTx(&LegacyTx{})
	SetSonicPayload(tx, "a", 1)
	SetSonicPayload(tx, "b", "hello")
	SetSonicPayload(tx, "c", true)

	if v, ok := GetSonicPayload[int](tx, "a"); !ok || v != 1 {
		t.Fatalf("key 'a': got %v, %v", v, ok)
	}
	if v, ok := GetSonicPayload[string](tx, "b"); !ok || v != "hello" {
		t.Fatalf("key 'b': got %v, %v", v, ok)
	}
	if v, ok := GetSonicPayload[bool](tx, "c"); !ok || v != true {
		t.Fatalf("key 'c': got %v, %v", v, ok)
	}
}

func TestSonicPayload_ReturnsFalseIfNotFound(t *testing.T) {
	tx := NewTx(&LegacyTx{})

	_, ok := GetSonicPayload[int](tx, "nonexistent")
	if ok {
		t.Fatal("expected ok to be false for unset key")
	}
}
