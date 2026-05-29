<p align="center">
<img src="./.github/logo.png" alt="logo" width="15%" />
</p>

# balance

A minimal, generic Golang library for implementing smooth weighted round-robin (SWRR) load balancing with dynamic self-healing, administrative controls, and Nginx-style effective weight degradation.

## Installation

```bash
go get github.com/mr-karan/balance
```

## Features

- **Go Generics**: Fully type-safe balancing for any `comparable` type (e.g., strings, connection pools, goroutine channels, or custom structs).
- **Nginx Effective Weight Algorithm**: Automatic degradation of selection priority on node failures (`RecordFailure`) and gradual recovery on success (`RecordSuccess`).
- **Dynamic Adjustments**: Update node weights (`UpdateWeight`) and toggle administrative active/inactive states (`SetActive`) on the fly.
- **Self-Healing Fallback**: Prevents total starvation by automatically resetting effective weights if all active nodes degrade to `0`.

---

## Usage

### 1. Basic Generic Round Robin

```go
package main

import (
    "fmt"
    "github.com/mr-karan/balance"
)

func main() {
    // Create a new generic load balancer for string IDs
    b := balance.NewBalance[string]()

    // Add items with their corresponding weights
    b.Add("server-a", 5)
    b.Add("server-b", 3)
    b.Add("server-c", 2)    
    
    // Get the next item (sequence will be smoothly balanced: a b c a a b a c b a)
    for i := 0; i < 10; i++ {
        fmt.Println(b.Get())
    }
}
```

### 2. Nginx-Style Failure Tracking (Effective Weights)

When targets fail, report errors to decrease their likelihood of selection. Once healthy again, report success to restore their original weight.

```go
// Record a failure: lowers the priority of server-a
b.RecordFailure("server-a")

// Balance continues, but server-a gets selected less frequently
target := b.Get()

// Record a success: restores the priority of server-a back to its base weight
b.RecordSuccess("server-a")
```

### 3. Dynamic Updates & Eviction

```go
// Dynamically adjust weight to 10
b.UpdateWeight("server-a", 10)

// Administratively deactivate server-b (will be skipped by Get() until reactivated)
b.SetActive("server-b", false)
```

---

## Algorithm & Trace

The algorithm is based on the [Smooth Weighted Round Robin](https://github.com/phusion/nginx/commit/27e94984486058d73157038f7950a0a36ecc6e35) used by NGINX.

### How it Works
On each peer selection we increase the `current_weight` of each eligible peer by its `effective_weight`, select the peer with the greatest `current_weight`, and reduce its `current_weight` by the total number of weight points distributed among peers.

For edge case weights like `{ 5, 1, 1 }`, this algorithm produces the smooth sequence `{ a, a, b, a, c, a, a }` instead of the unbalanced sequence `{ c, b, a, a, a, a, a }` produced by basic round robin.

### Step-by-Step Weight Tracing for `{ 5, 1, 1 }`
This shows the sequence of `current_weight` values after each selection:

```
     a  b  c
     0  0  0  (initial state)

     5  1  1  (a selected)
    -2  1  1

     3  2  2  (a selected)
    -4  2  2

     1  3  3  (b selected)
     1 -4  3

     6 -3  4  (a selected)
    -1 -3  4

     4 -2  5  (c selected)
     4 -2 -2

     9 -1 -1  (a selected)
     2 -1 -1

     7  0  0  (a selected)
     0  0  0
```

### Role of `effective_weight`
To preserve weight reduction in case of failures, the `effective_weight` variable is used. It usually matches the peer's configured `weight`, but is reduced temporarily on peer failures. This avoids loops with backup servers and prevents skipping alive upstreams when multiple dead ones exist.

---

## Real-World Application: Worker Pool

To see how this load balancer can be used in a real-world, concurrent Go worker pool (complete with circuit-breakers, metric-based dynamic weighting, and auto-recovery), run our simulation suite directly from the root:

```bash
go test -v -run TestWorkerPoolSimulation
```

This simulation runs concurrent tasks across workers with varying speeds and fault tolerances, demonstrating SWRR task routing, error-based eviction, and automatic worker recovery.

## Benchmark

Run the benchmarks locally:

```bash
go test -v -failfast -bench=. -benchmem -run=^$
```

Example output:
```
goos: linux
goarch: amd64
pkg: github.com/mr-karan/balance
cpu: 11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz
BenchmarkBalance
BenchmarkBalance/items-10
BenchmarkBalance/items-10-8             18249529                63.82 ns/op            0 B/op          0 allocs/op
BenchmarkBalance/items-100
BenchmarkBalance/items-100-8             9840943               119.5 ns/op             0 B/op          0 allocs/op
BenchmarkBalance/items-1000
BenchmarkBalance/items-1000-8            1608460               767.1 ns/op             0 B/op          0 allocs/op
BenchmarkBalance/items-10000
BenchmarkBalance/items-10000-8            123394              9621 ns/op               0 B/op          0 allocs/op
BenchmarkBalance/items-100000
BenchmarkBalance/items-100000-8            10000            102295 ns/op               0 B/op          0 allocs/op
PASS
ok      github.com/mr-karan/balance     7.927s
```
