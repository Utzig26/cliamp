package workgroup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func assertDone(t *testing.T, ctx context.Context, what string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("%s: context not cancelled", what)
	}
}

func TestStartAfterShutdownIsRefused(t *testing.T) {
	var g Group
	g.Shutdown()
	if _, _, err := g.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start() error = %v, want ErrClosed", err)
	}
	g.Close() // idempotent, nothing to wait for
}

func TestShutdownCancelsStartedWork(t *testing.T) {
	var g Group
	ctx, finish, err := g.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g.Shutdown()
	assertDone(t, ctx, "after Shutdown")
	finish()
	g.Wait()
}

func TestCancelAbandonsWorkButStaysOpen(t *testing.T) {
	var g Group
	stale, finishStale, err := g.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finishStale()
	g.Cancel()
	assertDone(t, stale, "after Cancel")

	fresh, finishFresh, err := g.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() after Cancel error = %v", err)
	}
	defer finishFresh()
	if fresh.Err() != nil {
		t.Fatal("work started after Cancel was already cancelled")
	}
}

func TestParentCancellationPropagates(t *testing.T) {
	var g Group
	parent, cancel := context.WithCancel(context.Background())
	ctx, finish, err := g.Start(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	cancel()
	assertDone(t, ctx, "after parent cancel")
}

func TestCloseWaitsForFinish(t *testing.T) {
	var g Group
	ctx, finish, err := g.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		g.Close()
		close(closed)
	}()
	assertDone(t, ctx, "after Close")
	select {
	case <-closed:
		t.Fatal("Close returned before finish")
	case <-time.After(50 * time.Millisecond):
	}
	go func() {
		<-released
		finish()
		finish() // idempotent
	}()
	close(released)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after finish")
	}
}

func TestStartRacesShutdownWithoutLosingWork(t *testing.T) {
	var g Group
	var callers sync.WaitGroup
	var finished sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			ctx, finish, err := g.Start(context.Background())
			if err != nil {
				return
			}
			finished.Add(1)
			go func() {
				defer finished.Done()
				<-ctx.Done()
				finish()
			}()
		}()
	}
	close(start)
	g.Close()
	callers.Wait()
	// Every admitted unit saw cancellation and finished before Close returned.
	done := make(chan struct{})
	go func() { finished.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("admitted work outlived Close")
	}
}
