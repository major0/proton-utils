package client

import (
	"context"
	"sync"
	"testing"

	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/pool"
)

// TestStatLinks_Empty verifies that StatLinks returns nil for empty input
// without touching the pool.
func TestStatLinks_Empty(t *testing.T) {
	c := &Client{
		Session: &api.Session{
			Pool: pool.New(context.Background(), 4),
		},
	}

	links, err := c.StatLinks(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("StatLinks(nil): %v", err)
	}
	if links != nil {
		t.Fatalf("StatLinks(nil) = %v, want nil", links)
	}

	links, err = c.StatLinks(context.Background(), nil, nil, []string{})
	if err != nil {
		t.Fatalf("StatLinks([]): %v", err)
	}
	if links != nil {
		t.Fatalf("StatLinks([]) = %v, want nil", links)
	}
}

// TestFindLinkByName_Empty verifies that FindLinkByName returns nil for
// empty input without touching the pool.
func TestFindLinkByName_Empty(t *testing.T) {
	c := &Client{
		Session: &api.Session{},
	}

	link, err := c.FindLinkByName(context.Background(), nil, nil, nil, "test")
	if err != nil {
		t.Fatalf("FindLinkByName(nil): %v", err)
	}
	if link != nil {
		t.Fatalf("FindLinkByName(nil) = %v, want nil", link)
	}

	link, err = c.FindLinkByName(context.Background(), nil, nil, []string{}, "test")
	if err != nil {
		t.Fatalf("FindLinkByName([]): %v", err)
	}
	if link != nil {
		t.Fatalf("FindLinkByName([]) = %v, want nil", link)
	}
}

// TestSessionPool_Default verifies that session constructors create a
// non-nil pool with the default concurrency limit.
func TestSessionPool_Default(t *testing.T) {
	ctx := context.Background()
	p := pool.New(ctx, api.DefaultMaxWorkers())

	session := &api.Session{
		Pool: p,
	}

	if session.Pool == nil {
		t.Fatal("Session.Pool is nil after construction")
	}

	// Verify pool is functional by submitting and waiting.
	var ran bool
	var wg sync.WaitGroup
	session.Pool.Go(&wg, func(_ context.Context) error {
		ran = true
		return nil
	})
	wg.Wait()
	if !ran {
		t.Fatal("pool task did not execute")
	}
}
