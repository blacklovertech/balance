package balance_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mr-karan/balance"
)

// Task represents a unit of work.
type Task[T any] struct {
	ID        string
	Payload   T
	Ctx       context.Context
	CreatedAt time.Time
}

// Result represents the outcome of a processed task.
type Result[R any] struct {
	TaskID    string
	Output    R
	Err       error
	WorkerID  string
	Duration  time.Duration
}

// Worker executes tasks and tracks metrics.
type Worker[T any, R any] struct {
	ID           string
	capacity     int // base weight capacity
	taskChan     chan Task[T]
	pool         *WorkerPool[T, R]
	fn           func(Task[T]) (R, error)

	// Metrics & state
	activeTasks  int32
	consecErrors int32
	totalLatency int64 // cumulative latency in microseconds (atomic)
	totalTasks   int64 // total tasks processed (atomic)
}

// NewWorker creates a new worker instance.
func NewWorker[T any, R any](id string, capacity int, fn func(Task[T]) (R, error)) *Worker[T, R] {
	return &Worker[T, R]{
		ID:       id,
		capacity: capacity,
		taskChan: make(chan Task[T], 100),
		fn:       fn,
	}
}

// Run listens for incoming tasks on the worker's channel.
func (w *Worker[T, R]) Run(ctx context.Context, results chan<- Result[R]) {
	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-w.taskChan:
			if !ok {
				return
			}
			atomic.AddInt32(&w.activeTasks, 1)
			start := time.Now()

			output, err := w.fn(task)

			duration := time.Since(start)
			atomic.AddInt32(&w.activeTasks, -1)
			atomic.AddInt64(&w.totalLatency, int64(duration/time.Microsecond))
			atomic.AddInt64(&w.totalTasks, 1)

			res := Result[R]{
				TaskID:   task.ID,
				Output:   output,
				Err:      err,
				WorkerID: w.ID,
				Duration: duration,
			}

			// Circuit Breaker & Effective Weight logic
			if err != nil {
				_ = w.pool.balancer.RecordFailure(w.ID) // smooth weight reduction
				consec := atomic.AddInt32(&w.consecErrors, 1)
				if consec >= w.pool.errThreshold {
					w.pool.deactivateWorker(w.ID, err)
				}
			} else {
				_ = w.pool.balancer.RecordSuccess(w.ID) // restore weight on success
				atomic.StoreInt32(&w.consecErrors, 0)
			}

			select {
			case <-ctx.Done():
				return
			case results <- res:
			}
		}
	}
}

// WorkerPool coordinates task dispatching, health checks, and dynamic load balancing.
type WorkerPool[T any, R any] struct {
	mu           sync.RWMutex
	workers      map[string]*Worker[T, R]
	balancer     *balance.Balance[string]
	tasks        chan Task[T]
	results      chan Result[R]
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	errThreshold int32
}

// NewWorkerPool instantiates a new generic worker pool.
func NewWorkerPool[T any, R any](errThreshold int32) *WorkerPool[T, R] {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool[T, R]{
		workers:      make(map[string]*Worker[T, R]),
		balancer:     balance.NewBalance[string](),
		tasks:        make(chan Task[T], 500),
		results:      make(chan Result[R], 500),
		ctx:          ctx,
		cancel:       cancel,
		errThreshold: errThreshold,
	}
}

// AddWorker registers a worker into the pool and starts its process loop.
func (p *WorkerPool[T, R]) AddWorker(w *Worker[T, R]) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.pool = p
	p.workers[w.ID] = w
	p.balancer.Add(w.ID, w.capacity)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		w.Run(p.ctx, p.results)
	}()
}

// Submit enqueues a task for processing.
func (p *WorkerPool[T, R]) Submit(task Task[T]) {
	p.tasks <- task
}

// Results returns the output channel for processed results.
func (p *WorkerPool[T, R]) Results() <-chan Result[R] {
	return p.results
}

// Start kicks off the dispatcher, dynamic weight ticker, and auto-recovery loop.
func (p *WorkerPool[T, R]) Start() {
	p.wg.Add(3)
	go p.dispatchLoop()
	go p.dynamicWeightLoop()
	go p.autoRecoveryLoop()
}

// Stop gracefully shuts down the worker pool, cancelling all background loops.
func (p *WorkerPool[T, R]) Stop() {
	p.cancel()
	p.wg.Wait()
	close(p.results)
}

// dispatchLoop distributes incoming tasks using the smooth weighted round robin balancer.
func (p *WorkerPool[T, R]) dispatchLoop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case task, ok := <-p.tasks:
			if !ok {
				return
			}

			var targetWorker *Worker[T, R]
			for {
				workerID := p.balancer.Get()
				if workerID == "" {
					// No active workers, wait for a worker to recover
					time.Sleep(5 * time.Millisecond)
					continue
				}
				p.mu.RLock()
				w, exists := p.workers[workerID]
				p.mu.RUnlock()
				if exists {
					targetWorker = w
					break
				}
			}

			select {
			case <-p.ctx.Done():
				return
			case targetWorker.taskChan <- task:
			}
		}
	}
}

// deactivateWorker evicts a worker from the load balancing pool (circuit breaker).
func (p *WorkerPool[T, R]) deactivateWorker(id string, reason error) {
	fmt.Printf("\n[Circuit Breaker 🚨] Evicting worker %s: consecutive errors exceeded threshold. Reason: %v\n", id, reason)
	p.balancer.SetActive(id, false)
}

// dynamicWeightLoop adjusts worker weights dynamically based on active task loads and response latencies.
func (p *WorkerPool[T, R]) dynamicWeightLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			for id, w := range p.workers {
				// Don't adjust weights of deactivated workers
				consec := atomic.LoadInt32(&w.consecErrors)
				if consec >= p.errThreshold {
					continue
				}

				active := atomic.LoadInt32(&w.activeTasks)
				latency := atomic.LoadInt64(&w.totalLatency)
				tasks := atomic.LoadInt64(&w.totalTasks)

				avgLatencyMs := int64(0)
				if tasks > 0 {
					avgLatencyMs = (latency / tasks) / 1000 // convert micro to milli
				}

				// Weight penalty = active tasks count + latency penalty
				penalty := int(active)*3 + int(avgLatencyMs/10)
				newWeight := w.capacity - penalty
				if newWeight < 1 {
					newWeight = 1
				}

				p.balancer.UpdateWeight(id, newWeight)
			}
			p.mu.Unlock()
		}
	}
}

// autoRecoveryLoop attempts to heal deactivated workers by probing their execution function.
func (p *WorkerPool[T, R]) autoRecoveryLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			for id, w := range p.workers {
				consecErr := atomic.LoadInt32(&w.consecErrors)
				if consecErr >= p.errThreshold {
					fmt.Printf("[Auto Recovery 🩺] Probing inactive worker %s to check health status...\n", id)

					// Probe the worker function directly with a test task
					probeTask := Task[T]{
						ID:        "probe-" + id,
						CreatedAt: time.Now(),
					}

					_, err := w.fn(probeTask)
					if err == nil {
						fmt.Printf("[Auto Recovery ✅] Worker %s has recovered! Restoring to load balancer.\n", id)
						atomic.StoreInt32(&w.consecErrors, 0)
						p.balancer.SetActive(id, true)
					} else {
						fmt.Printf("[Auto Recovery ❌] Worker %s probe failed: %v\n", id, err)
					}
				}
			}
			p.mu.Unlock()
		}
	}
}

func TestWorkerPoolSimulation(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	fmt.Println("=========================================================================")
	fmt.Println("🚀 Starting Load-Balanced Worker Pool Simulation using smooth-round-robin")
	fmt.Println("=========================================================================")

	// Define a task processing function.
	// Worker-1 is perfect.
	// Worker-2 is slower.
	// Worker-3 is faulty (periodically returns errors), simulating network drops.
	workerFunc := func(workerID string) func(Task[int]) (string, error) {
		return func(t Task[int]) (string, error) {
			// Simulate processing time
			if workerID == "Worker-2" {
				time.Sleep(30 * time.Millisecond) // Slow worker
			} else {
				time.Sleep(10 * time.Millisecond) // Fast worker
			}

			// Simulate failures for Worker-3 (Faulty worker)
			if workerID == "Worker-3" {
				// Don't fail the probe task so it can recover
				if t.ID != "probe-Worker-3" && rand.Float32() < 0.7 {
					return "", errors.New("database connection timeout")
				}
			}

			return fmt.Sprintf("Processed payload %d", t.Payload), nil
		}
	}

	// Create pool. Circuit break threshold set to 3.
	pool := NewWorkerPool[int, string](3)

	// Add workers with capacities (weights):
	// Worker-1 (Capacity 10) - High capability
	// Worker-2 (Capacity 5)  - Medium capability
	// Worker-3 (Capacity 8)  - High capability but prone to failures
	pool.AddWorker(NewWorker("Worker-1", 10, workerFunc("Worker-1")))
	pool.AddWorker(NewWorker("Worker-2", 5, workerFunc("Worker-2")))
	pool.AddWorker(NewWorker("Worker-3", 8, workerFunc("Worker-3")))

	pool.Start()

	// Start a goroutine to read results
	resultCounts := make(map[string]int)
	var mu sync.Mutex
	go func() {
		for res := range pool.Results() {
			mu.Lock()
			if res.Err == nil {
				resultCounts[res.WorkerID]++
			}
			mu.Unlock()
		}
	}()

	// Submit tasks rapidly to simulate real load
	totalTasks := 60
	fmt.Printf("Submitting %d tasks into the pool...\n", totalTasks)
	for i := 1; i <= totalTasks; i++ {
		pool.Submit(Task[int]{
			ID:      fmt.Sprintf("task-%d", i),
			Payload: i * 100,
		})
		time.Sleep(15 * time.Millisecond)
	}

	// Give the remaining tasks time to complete, and trigger auto-recovery logs
	fmt.Println("\nWaiting for remaining tasks to complete and auto-recovery to run...")
	time.Sleep(3 * time.Second)

	pool.Stop()

	// Output final distribution
	fmt.Println("\n=========================================================================")
	fmt.Println("📊 Final Successful Task Distribution across Workers:")
	fmt.Println("=========================================================================")
	mu.Lock()
	for workerID, count := range resultCounts {
		fmt.Printf("- %s: %d successful tasks\n", workerID, count)
	}
	mu.Unlock()
	fmt.Println("Simulation completed successfully.")
}
