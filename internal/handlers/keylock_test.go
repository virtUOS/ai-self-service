package handlers

import (
	"context"
	"testing"
	"time"
)

func TestKeyedLockExcludesSameUser(t *testing.T) {
	var l keyedLock
	unlock, err := l.Lock(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan func(), 1)
	go func() {
		u, err := l.Lock(context.Background(), 1)
		if err != nil {
			t.Error(err)
			return
		}
		got <- u
	}()
	select {
	case <-got:
		t.Fatal("second Lock for the same user did not wait")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	select {
	case u := <-got:
		u()
	case <-time.After(time.Second):
		t.Fatal("second Lock never acquired after release")
	}
}

func TestKeyedLockDoesNotBlockOtherUsers(t *testing.T) {
	var l keyedLock
	unlock, err := l.Lock(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	other, err := l.Lock(ctx, 2)
	if err != nil {
		t.Fatalf("lock for another user: %v", err)
	}
	other()
}

func TestKeyedLockWaiterGivesUpWhenContextEnds(t *testing.T) {
	var l keyedLock
	unlock, err := l.Lock(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.Lock(ctx, 1); err == nil {
		t.Fatal("Lock returned no error after its context ended")
	}
}

func TestKeyedLockForgetsIdleUsers(t *testing.T) {
	var l keyedLock
	unlock, err := l.Lock(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	unlock() // releasing twice must not corrupt the count

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u, _ := l.Lock(context.Background(), 2)
	if _, err := l.Lock(ctx, 2); err == nil {
		t.Fatal("expected a cancelled waiter to fail")
	}
	u()

	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.entries); n != 0 {
		t.Errorf("%d entries left after every lock was released", n)
	}
}
