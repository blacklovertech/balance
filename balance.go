package balance

import (
	"errors"
	"sync"
)

var (
	// ErrDuplicateID error is thrown when attempt to add an ID
	// which is already added to the balancer.
	ErrDuplicateID = errors.New("entry already added")
	// ErrIDNotFound is thrown when removing a non-existent ID.
	ErrIDNotFound = errors.New("id not found")
)

// Balance represents a smooth weighted round-robin load balancer.
type Balance[T comparable] struct {
	sync.RWMutex

	// items is the list of items to balance
	items []*Item[T]
	// next is the next item to use.
	next *Item[T]
}

// NewBalance creates a new load balancer.
func NewBalance[T comparable]() *Balance[T] {
	return &Balance[T]{
		items: make([]*Item[T], 0),
	}
}

// Item represents the item in the list.
type Item[T comparable] struct {
	// id is the id of the item.
	id T
	// weight is the weight of the item that is given by the user.
	weight int
	// effective is the current effective weight of the item (reduced temporarily on failures).
	effective int
	// current is the current running weight of the item in SWRR selection.
	current int
	// active determines if the item should be considered in balancing.
	active bool
}

// NewItem creates a new item with active status set to true.
func NewItem[T comparable](id T, weight int) *Item[T] {
	return &Item[T]{
		id:        id,
		weight:    weight,
		effective: weight,
		current:   0,
		active:    true,
	}
}

// Add appends a new item with its corresponding weight to the balancer.
func (b *Balance[T]) Add(id T, weight int) error {
	b.Lock()
	defer b.Unlock()
	for _, v := range b.items {
		if v.id == id {
			return ErrDuplicateID
		}
	}

	b.items = append(b.items, NewItem(id, weight))

	return nil
}

// Get selects and returns the next item from the balancer using the
// Smooth Weighted Round-Robin algorithm with Nginx effective weight adjustments.
func (b *Balance[T]) Get() T {
	b.Lock()
	defer b.Unlock()

	var zero T

	// Filter active items
	var activeItems []*Item[T]
	for _, item := range b.items {
		if item.active {
			activeItems = append(activeItems, item)
		}
	}

	if len(activeItems) == 0 {
		return zero
	}

	// Calculate total effective weight
	var total int
	for _, item := range activeItems {
		total += item.effective
	}

	// Self-healing fallback: If all active nodes have degraded to 0 effective weight,
	// restore their effective weights back to their base weights to avoid starvation.
	if total == 0 {
		for _, item := range activeItems {
			item.effective = item.weight
			item.current = 0
			total += item.effective
		}
	}

	// SWRR selection using effective weight instead of base weight
	var max *Item[T]
	for _, item := range activeItems {
		item.current += item.effective

		// Select the item with max current weight.
		if max == nil || item.current > max.current {
			max = item
		}
	}

	if max == nil {
		return zero
	}

	// Select the item with the max weight.
	b.next = max
	// Reduce the current weight of the selected item by the total weight.
	max.current -= total

	return max.id
}

// Remove deletes an item by ID from the balancer.
func (b *Balance[T]) Remove(id T) error {
	b.Lock()
	defer b.Unlock()

	for i, item := range b.items {
		if item.id == id {
			b.items = append(b.items[:i], b.items[i+1:]...)
			return nil
		}
	}

	return ErrIDNotFound
}

// ItemIDs returns a list of all item IDs in the balancer.
func (b *Balance[T]) ItemIDs() []T {
	b.RLock()
	defer b.RUnlock()

	ids := make([]T, len(b.items))
	for i, item := range b.items {
		ids[i] = item.id
	}
	return ids
}

// UpdateWeight updates the weight of an item dynamically and resets its effective weight.
func (b *Balance[T]) UpdateWeight(id T, weight int) error {
	b.Lock()
	defer b.Unlock()

	for _, item := range b.items {
		if item.id == id {
			item.weight = weight
			item.effective = weight
			return nil
		}
	}

	return ErrIDNotFound
}

// SetActive toggles the active status of an item.
func (b *Balance[T]) SetActive(id T, active bool) error {
	b.Lock()
	defer b.Unlock()

	for _, item := range b.items {
		if item.id == id {
			item.active = active
			// Reset current weight if deactivating to prevent stale build-up
			if !active {
				item.current = 0
				item.effective = item.weight
			}
			return nil
		}
	}

	return ErrIDNotFound
}

// RecordFailure decreases the effective weight of an item by 1, to a minimum of 0.
func (b *Balance[T]) RecordFailure(id T) error {
	b.Lock()
	defer b.Unlock()

	for _, item := range b.items {
		if item.id == id {
			if item.effective > 0 {
				item.effective--
			}
			return nil
		}
	}

	return ErrIDNotFound
}

// RecordSuccess increases the effective weight of an item by 1, to a maximum of its configured weight.
func (b *Balance[T]) RecordSuccess(id T) error {
	b.Lock()
	defer b.Unlock()

	for _, item := range b.items {
		if item.id == id {
			if item.effective < item.weight {
				item.effective++
			}
			return nil
		}
	}

	return ErrIDNotFound
}
