package balance_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mr-karan/balance"
)

func TestBalance(t *testing.T) {
	// Test Init.
	t.Run("init", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		if bl.Get() != "" {
			t.Error("Expected empty string")
		}
	})

	// Test round robin.
	t.Run("round robin", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 1)
		bl.Add("b", 1)
		bl.Add("c", 1)
		result := make(map[string]int)
		for i := 0; i < 999; i++ {
			result[bl.Get()]++
		}

		if result["a"] != 333 || result["b"] != 333 || result["c"] != 333 {
			t.Error("Wrong counts", result)
		}
	})

	t.Run("adding duplicate entry", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		err := bl.Add("c", 1)
		if !errors.Is(err, nil) {
			t.Error("Wrong error received", err.Error())
		}

		err = bl.Add("c", 1)
		if !errors.Is(err, balance.ErrDuplicateID) {
			t.Error("Wrong error received", err.Error())
		}
	})

	// Test weighted.
	t.Run("weighted custom split", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 2)
		bl.Add("b", 1)
		bl.Add("c", 1)
		result := make(map[string]int)
		for i := 0; i < 1000; i++ {
			result[bl.Get()]++
		}

		if result["a"] != 500 || result["b"] != 250 || result["c"] != 250 {
			t.Error("Wrong counts", result)
		}
	})

	t.Run("weighted another custom split", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 5)
		bl.Add("b", 3)
		bl.Add("c", 2)
		result := make(map[string]int)
		for i := 0; i < 1000; i++ {
			result[bl.Get()]++
		}

		if result["a"] != 500 || result["b"] != 300 || result["c"] != 200 {
			t.Error("Wrong counts", result)
		}
	})

	// Test with one item as zero weight.
	t.Run("weighted with zero", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 0)
		bl.Add("b", 1)
		bl.Add("c", 1)
		result := make(map[string]int)
		for i := 0; i < 1000; i++ {
			result[bl.Get()]++
		}

		if result["a"] != 0 || result["b"] != 500 || result["c"] != 500 {
			t.Error("Wrong counts", result)
		}
	})

	// Test remove item.
	t.Run("remove item", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 1)
		bl.Add("b", 1)
		bl.Add("c", 1)

		err := bl.Remove("b")
		if err != nil {
			t.Error("Expected no error, got", err)
		}

		ids := bl.ItemIDs()
		expected := map[string]bool{"a": true, "c": true}
		for _, id := range ids {
			if !expected[id] {
				t.Error("Unexpected ID in list", id)
			}
		}

		// Ensure removed item isn't returned by a Get.
		for i := 0; i < 100; i++ {
			if bl.Get() == "b" {
				t.Error("Removed item 'b' still returned by Get")
			}
		}
	})

	// Test remove non-existent item.
	t.Run("remove non-existent item", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 1)
		err := bl.Remove("x")
		if !errors.Is(err, balance.ErrIDNotFound) {
			t.Error("Expected ErrIDNotFound, got", err)
		}
	})

	// Test list items ids.
	t.Run("list items", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("x", 3)
		bl.Add("y", 2)

		ids := bl.ItemIDs()
		expected := map[string]bool{"x": true, "y": true}
		for _, id := range ids {
			if !expected[id] {
				t.Error("Unexpected ID in list", id)
			}
		}

		if len(ids) != 2 {
			t.Error("Expected 2 items, got", len(ids))
		}
	})

	// Test dynamic weight updates.
	t.Run("dynamic weight update", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 1)
		bl.Add("b", 1)

		// Before update, they should distribute equally (50/50)
		result := make(map[string]int)
		for i := 0; i < 100; i++ {
			result[bl.Get()]++
		}
		if result["a"] != 50 || result["b"] != 50 {
			t.Errorf("Expected equal distribution (50/50), got a=%d, b=%d", result["a"], result["b"])
		}

		// Update weight of a to 3. Distribution should become 3:1 (75/25)
		err := bl.UpdateWeight("a", 3)
		if err != nil {
			t.Fatalf("Failed to update weight: %v", err)
		}

		result = make(map[string]int)
		for i := 0; i < 100; i++ {
			result[bl.Get()]++
		}
		if result["a"] != 75 || result["b"] != 25 {
			t.Errorf("Expected 3:1 distribution (75/25), got a=%d, b=%d", result["a"], result["b"])
		}

		// Try updating non-existent item
		err = bl.UpdateWeight("non-existent", 5)
		if !errors.Is(err, balance.ErrIDNotFound) {
			t.Errorf("Expected ErrIDNotFound, got %v", err)
		}
	})

	// Test active/inactive status toggles.
	t.Run("set active status", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 3)
		bl.Add("b", 1)

		// Deactivate 'a'. Get should only return 'b'.
		err := bl.SetActive("a", false)
		if err != nil {
			t.Fatalf("Failed to set active status: %v", err)
		}

		for i := 0; i < 100; i++ {
			if res := bl.Get(); res != "b" {
				t.Errorf("Expected only 'b' to be selected, got %q", res)
			}
		}

		// Reactivate 'a'. Distribution should resume as 3:1
		err = bl.SetActive("a", true)
		if err != nil {
			t.Fatalf("Failed to set active status: %v", err)
		}

		result := make(map[string]int)
		for i := 0; i < 100; i++ {
			result[bl.Get()]++
		}
		if result["a"] != 75 || result["b"] != 25 {
			t.Errorf("Expected 3:1 distribution (75/25), got a=%d, b=%d", result["a"], result["b"])
		}

		// Deactivate all items. Get should return zero value (empty string)
		bl.SetActive("a", false)
		bl.SetActive("b", false)
		if res := bl.Get(); res != "" {
			t.Errorf("Expected empty string when all items are inactive, got %q", res)
		}

		// Try setting active status for non-existent item
		err = bl.SetActive("non-existent", true)
		if !errors.Is(err, balance.ErrIDNotFound) {
			t.Errorf("Expected ErrIDNotFound, got %v", err)
		}
	})

	// Test RecordFailure and RecordSuccess.
	t.Run("effective weight failures and success", func(t *testing.T) {
		bl := balance.NewBalance[string]()
		bl.Add("a", 5)
		bl.Add("b", 1)

		// Record a failure for "a". Its effective weight should go down from 5 to 4.
		err := bl.RecordFailure("a")
		if err != nil {
			t.Fatalf("Failed to record failure: %v", err)
		}

		// Verify distribution changes based on effective weight (4:1 split)
		result := make(map[string]int)
		for i := 0; i < 50; i++ {
			result[bl.Get()]++
		}
		if result["a"] != 40 || result["b"] != 10 {
			t.Errorf("Expected 4:1 distribution (40/10), got a=%d, b=%d", result["a"], result["b"])
		}

		// Record success to restore its weight back to 5
		err = bl.RecordSuccess("a")
		if err != nil {
			t.Fatalf("Failed to record success: %v", err)
		}

		// Verify distribution changes back to 5:1 split
		result = make(map[string]int)
		for i := 0; i < 60; i++ {
			result[bl.Get()]++
		}
		if result["a"] != 50 || result["b"] != 10 {
			t.Errorf("Expected 5:1 distribution (50/10), got a=%d, b=%d", result["a"], result["b"])
		}

		// Try recording failure/success for non-existent item
		err = bl.RecordFailure("non-existent")
		if !errors.Is(err, balance.ErrIDNotFound) {
			t.Errorf("Expected ErrIDNotFound, got %v", err)
		}
		err = bl.RecordSuccess("non-existent")
		if !errors.Is(err, balance.ErrIDNotFound) {
			t.Errorf("Expected ErrIDNotFound, got %v", err)
		}
	})
}

func TestBalance_Concurrent(t *testing.T) {
	t.Run("concurrent", func(t *testing.T) {
		var (
			a, b, c int64
		)
		bl := balance.NewBalance[string]()
		bl.Add("a", 1)
		bl.Add("b", 1)
		bl.Add("c", 1)

		var wg sync.WaitGroup

		for i := 0; i < 999; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				switch bl.Get() {
				case "a":
					atomic.AddInt64(&a, 1)
				case "b":
					atomic.AddInt64(&b, 1)
				case "c":
					atomic.AddInt64(&c, 1)
				default:
					t.Error("Wrong item")
				}
			}()
		}

		wg.Wait()

		if a != 333 || b != 333 || c != 333 {
			t.Error("Wrong counts", a, b, c)
		}
	})
}
