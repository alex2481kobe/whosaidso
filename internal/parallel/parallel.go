// Package parallel runs a ledger's independent per-item work (reading and
// hashing a file, decoding a bundle or an event) on a few goroutines. It
// decides nothing about a bundle: callers still check order, the chain and the
// fold sequentially, in order, after it returns.
package parallel

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// IOWorkers is the limit for work that opens files: opening thousands of files
// in one directory contends in the kernel, and measured reads gain nothing
// past a few workers.
const IOWorkers = 4

// Do runs fn(0) .. fn(n-1) on up to limit goroutines (GOMAXPROCS when limit is
// not positive) and returns the error of the lowest index that failed, which is
// the error a sequential loop would have stopped at. fn must write only its own
// index's results.
func Do(n, limit int, fn func(i int) error) error {
	workers := runtime.GOMAXPROCS(0)
	if limit > 0 {
		workers = min(workers, limit)
	}
	workers = min(workers, n)
	if workers <= 1 {
		for i := 0; i < n; i++ {
			if err := fn(i); err != nil {
				return err
			}
		}
		return nil
	}
	errs := make([]error, n)
	var next atomic.Int64
	var failed atomic.Int64
	failed.Store(int64(n))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				// Indexes past a failure cannot change which error is returned.
				if i >= n || int64(i) > failed.Load() {
					return
				}
				if err := fn(i); err != nil {
					errs[i] = err
					for old := failed.Load(); int64(i) < old && !failed.CompareAndSwap(old, int64(i)); old = failed.Load() {
					}
				}
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
