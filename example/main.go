package main

import (
	"fmt"
	"log"

	balance "github.com/mr-karan/balance"
)

func main() {
	b := balance.NewBalance[string]()

	// Add items with weights; handle duplicate errors.
	idsAndWeights := map[string]int{
		"a": 5,
		"b": 3,
		"c": 2,
	}
	for id, w := range idsAndWeights {
		if err := b.Add(id, w); err != nil {
			log.Printf("failed to add %s: %v", id, err)
		}
	}
	// Trying to add duplicate
	if err := b.Add("a", 10); err != nil {
		fmt.Println("expected error:", err)
	}

	// Simulate a few successful/failed calls to demonstrate effective weight adjustments.
	// "a" succeeds twice, "b" fails once, "c" remains untouched.
	b.RecordSuccess("a")
	b.RecordSuccess("a")
	b.RecordFailure("b")

	// Temporarily deactivate "c"
	b.SetActive("c", false)

	fmt.Println("Initial distribution (10 calls):")
	for i := 0; i < 10; i++ {
		item := b.Get()
		fmt.Printf("%s ", item)
	}
	fmt.Println()

	// Reactivate "c"
	b.SetActive("c", true)

	// Update weight of "b" dynamically
	b.UpdateWeight("b", 5)

	fmt.Println("After weight update & reactivation (10 calls):")
	for i := 0; i < 10; i++ {
		item := b.Get()
		fmt.Printf("%s ", item)
	}
	fmt.Println()

	// Remove an item
	b.Remove("a")

	fmt.Println("After removing 'a' (10 calls):")
	for i := 0; i < 10; i++ {
		item := b.Get()
		fmt.Printf("%s ", item)
	}
	fmt.Println()

	// List current items
	fmt.Println("Remaining IDs:", b.ItemIDs())
}