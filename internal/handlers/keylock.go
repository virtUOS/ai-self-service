package handlers

import (
	"context"
	"sync"
)

// keyedLock is a mutex per user ID. The key handlers read the user's key,
// change it upstream and then record the result; two of them running at once
// for the same user (a double-click) would both act on the same old key and
// leave one new key live in the gateway with nothing pointing at it.
//
// It waits on a channel rather than a sync.Mutex so a request whose client
// has gone away stops queuing behind a slow gateway call. The zero value is
// ready to use. An entry exists only while someone holds or awaits it, so the
// map does not grow with the number of users.
type keyedLock struct {
	mu      sync.Mutex
	entries map[int64]*lockEntry
}

type lockEntry struct {
	ch   chan struct{} // holds one token; whoever takes it owns the lock
	refs int           // holder plus waiters
}

// Lock waits for the user's lock and returns the function that releases it.
// It fails with ctx's error if ctx ends first.
func (l *keyedLock) Lock(ctx context.Context, userID int64) (unlock func(), err error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[int64]*lockEntry)
	}
	e := l.entries[userID]
	if e == nil {
		e = &lockEntry{ch: make(chan struct{}, 1)}
		l.entries[userID] = e
	}
	e.refs++
	l.mu.Unlock()

	select {
	case e.ch <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-e.ch; l.release(userID, e) }) }, nil
	case <-ctx.Done():
		l.release(userID, e)
		return nil, ctx.Err()
	}
}

func (l *keyedLock) release(userID int64, e *lockEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.refs--; e.refs == 0 {
		delete(l.entries, userID)
	}
}
