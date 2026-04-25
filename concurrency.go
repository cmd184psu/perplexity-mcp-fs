package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

var (
	allowedRoots []string
	rootsMu      sync.RWMutex
	logger       = log.New(io.Discard, "", 0)
	maxWorkers   = 20
)

// workerTimeout: if all worker slots are busy for this long, crash.
// On an M4 with NVMe this should never fire under any realistic workload.
const workerTimeout = 10 * time.Second

// opSlowThreshold: log a WARNING if an individual file op exceeds this.
const opSlowThreshold = 500 * time.Millisecond

// gateTimeout: maximum time a write or read will wait for the gate.
// If a stuck write somehow holds the gate beyond this, we log and return an
// error — we do NOT crash, because the read/write itself may still complete.
const gateTimeout = 30 * time.Second

// workerSem is a counting semaphore that caps concurrent file-I/O goroutines.
// Initialised here so tests use it without calling main().
// main() re-creates it after flag.Parse() so -workers N takes effect.
var workerSem = make(chan struct{}, maxWorkers)

// acquireWorker claims one worker slot. Crashes the process if no slot opens
// within workerTimeout — a full pool at that point means something is stuck.
func acquireWorker() {
	select {
	case workerSem <- struct{}{}:
		// fast path
	case <-time.After(workerTimeout):
		logger.Fatalf("TIMEOUT: acquireWorker waited >%v — worker pool exhausted (%d busy); crashing",
			workerTimeout, cap(workerSem))
	}
}

func releaseWorker() { <-workerSem }

// ── per-path file gate ─────────────────────────────────────────────────────────────

// fileGate serialises writes and lets reads wait for in-progress writes.
// Design rules:
//   - All fields guarded by mu.
//   - pendingWrites counts goroutines that have claimed or are waiting to claim
//     exclusive write access.
//   - refs counts live get/put pairs; gate is evicted from the manager when
//     refs reaches zero.
//   - timedOut is set by the watchdog when a waiter exceeds gateTimeout;
//     any Wait loop checks it and returns early.
type fileGate struct {
	mu            sync.Mutex
	cond          *sync.Cond
	pendingWrites int
	refs          int
	timedOut      bool
}

type fileGateManager struct {
	mu    sync.Mutex
	gates map[string]*fileGate
}

func newFileGateManager() *fileGateManager {
	return &fileGateManager{gates: make(map[string]*fileGate)}
}

func (m *fileGateManager) get(path string) *fileGate {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.gates[path]
	if !ok {
		g = &fileGate{}
		g.cond = sync.NewCond(&g.mu)
		m.gates[path] = g
	}
	g.refs++
	return g
}

func (m *fileGateManager) put(path string, g *fileGate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g.refs--
	if g.refs == 0 {
		delete(m.gates, path)
	}
}

var fileGates = newFileGateManager()

// scheduleWatchdog fires a broadcast on gate after d, setting timedOut = true.
// This unblocks any cond.Wait loops that check timedOut so they can return
// an error instead of waiting forever.  The watchdog goroutine is cheap and
// short-lived; it does not hold any lock for longer than a Broadcast.
func scheduleWatchdog(gate *fileGate, d time.Duration) func() {
	timer := time.AfterFunc(d, func() {
		gate.mu.Lock()
		gate.timedOut = true
		gate.cond.Broadcast()
		gate.mu.Unlock()
	})
	return func() { timer.Stop() }
}

// withQueuedWriteCtx runs fn exclusively for path, serialising concurrent
// writers.  ctx cancellation and gateTimeout both cause an early return;
// in those cases pendingWrites is still correctly decremented so no future
// operations on the same path are blocked.
func withQueuedWriteCtx(ctx context.Context, path string, fn func() (string, error)) (string, error) {
	gate := fileGates.get(path)
	defer fileGates.put(path, gate)

	// --- acquire exclusive write slot ---
	cancelWatchdog := scheduleWatchdog(gate, gateTimeout)
	// Broadcast on ctx cancellation so any cond.Wait() wakes up immediately.
	ctxDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			gate.mu.Lock()
			gate.cond.Broadcast()
			gate.mu.Unlock()
		case <-ctxDone:
		}
	}()

	gate.mu.Lock()
	gate.pendingWrites++
	for gate.pendingWrites > 1 && !gate.timedOut && ctx.Err() == nil {
		gate.cond.Wait()
	}

	if ctx.Err() != nil {
		gate.pendingWrites--
		gate.cond.Broadcast()
		gate.mu.Unlock()
		close(ctxDone)
		cancelWatchdog()
		return "", fmt.Errorf("write cancelled for %s: %v", path, ctx.Err())
	}

	if gate.timedOut {
		gate.pendingWrites--
		gate.cond.Broadcast()
		gate.mu.Unlock()
		close(ctxDone)
		cancelWatchdog()
		logger.Printf("TIMEOUT: write gate on %s exceeded %v", path, gateTimeout)
		return "", fmt.Errorf("write gate timeout for %s after %v", path, gateTimeout)
	}
	gate.mu.Unlock()
	close(ctxDone)
	cancelWatchdog()

	// --- run fn with panic-safe cleanup ---
	var result string
	var fnErr error
	func() {
		defer func() {
			gate.mu.Lock()
			gate.pendingWrites--
			gate.cond.Broadcast()
			gate.mu.Unlock()
		}()
		start := time.Now()
		result, fnErr = fn()
		if elapsed := time.Since(start); elapsed > opSlowThreshold {
			logger.Printf("SLOW write on %s: %v", path, elapsed.Round(time.Millisecond))
		}
	}()
	return result, fnErr
}

// withQueuedWrite is the context-free convenience wrapper.
func withQueuedWrite(path string, fn func() (string, error)) (string, error) {
	return withQueuedWriteCtx(context.Background(), path, fn)
}

// withConsistentReadCtx waits for any in-progress write to finish, then runs
// fn.  fn runs outside the gate lock so concurrent reads are fully parallel.
func withConsistentReadCtx(ctx context.Context, path string, fn func() (string, error)) (string, error) {
	gate := fileGates.get(path)
	defer fileGates.put(path, gate)

	cancelWatchdog := scheduleWatchdog(gate, gateTimeout)
	// Broadcast on ctx cancellation so any cond.Wait() wakes up immediately.
	ctxDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			gate.mu.Lock()
			gate.cond.Broadcast()
			gate.mu.Unlock()
		case <-ctxDone:
		}
	}()

	gate.mu.Lock()
	for gate.pendingWrites > 0 && !gate.timedOut && ctx.Err() == nil {
		gate.cond.Wait()
	}

	if ctx.Err() != nil {
		gate.mu.Unlock()
		close(ctxDone)
		cancelWatchdog()
		return "", fmt.Errorf("read cancelled for %s: %v", path, ctx.Err())
	}

	if gate.timedOut {
		gate.mu.Unlock()
		close(ctxDone)
		cancelWatchdog()
		logger.Printf("TIMEOUT: read gate on %s exceeded %v", path, gateTimeout)
		return "", fmt.Errorf("read gate timeout for %s after %v", path, gateTimeout)
	}
	gate.mu.Unlock()
	close(ctxDone)
	cancelWatchdog()

	start := time.Now()
	result, err := fn()
	if elapsed := time.Since(start); elapsed > opSlowThreshold {
		logger.Printf("SLOW read on %s: %v", path, elapsed.Round(time.Millisecond))
	}
	return result, err
}

// withConsistentRead is the context-free convenience wrapper.
func withConsistentRead(path string, fn func() (string, error)) (string, error) {
	return withConsistentReadCtx(context.Background(), path, fn)
}
