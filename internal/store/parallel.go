package store

// One helper for the ledger's independent per-file work (read and hash, or
// decode). It decides nothing about a bundle: callers still check order, the
// chain and the fold sequentially, in file order, after it returns.

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// maxWorkers caps the goroutines: opening thousands of files in one directory
// contends in the kernel, and measured reads gain nothing past a few workers.
const maxWorkers = 4

// parallelDo runs fn(0) .. fn(n-1) on up to maxWorkers goroutines and returns
// the error of the lowest index that failed, which is the error a sequential
// loop would have stopped at. fn must write only its own index's results.
func parallelDo(n int, fn func(i int) error) error {
	workers := min(runtime.GOMAXPROCS(0), maxWorkers, n)
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
