// Package sessiontesting ports harness/session/testing: storage decorators,
// instrumentation, gating, and runner-independent conformance suites.
package sessiontesting

import (
	"fmt"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
)

// StorageDecorator is the forwarding base for decorators that alter one
// part of Storage behavior.
type StorageDecorator struct {
	Delegate session.Storage
}

func (d *StorageDecorator) Commit(writes []session.Write, ctx harness.Context) (session.CommitResult, error) {
	return d.Delegate.Commit(writes, ctx)
}
func (d *StorageDecorator) GetEntries(ids []string, ctx harness.Context) (map[string]*session.Entry, error) {
	return d.Delegate.GetEntries(ids, ctx)
}
func (d *StorageDecorator) GetValue(address session.Value, ctx harness.Context) (*session.StoredValue, error) {
	return d.Delegate.GetValue(address, ctx)
}
func (d *StorageDecorator) ScanValues(prefix session.Value, ctx harness.Context) ([]session.StoredValue, error) {
	return d.Delegate.ScanValues(prefix, ctx)
}
func (d *StorageDecorator) ReadList(address session.ValueList, options *session.ListReadOptions, ctx harness.Context) ([]session.ListElement, error) {
	return d.Delegate.ReadList(address, options, ctx)
}
func (d *StorageDecorator) ScanBranch(query session.StorageBranchScan, ctx harness.Context) ([]*session.Entry, error) {
	return d.Delegate.ScanBranch(query, ctx)
}
func (d *StorageDecorator) ScanBranchStructure(query session.StorageBranchScan, ctx harness.Context) ([]session.EntryStructure, error) {
	return d.Delegate.ScanBranchStructure(query, ctx)
}
func (d *StorageDecorator) ScanEntries(query session.EntryScan, ctx harness.Context) ([]*session.Entry, error) {
	return d.Delegate.ScanEntries(query, ctx)
}
func (d *StorageDecorator) ScanUsage(query session.UsageScan, ctx harness.Context) ([]session.UsageRow, error) {
	return d.Delegate.ScanUsage(query, ctx)
}
func (d *StorageDecorator) GetStats(ctx harness.Context) (session.SessionStats, error) {
	return d.Delegate.GetStats(ctx)
}
func (d *StorageDecorator) Close(ctx harness.Context) error { return d.Delegate.Close(ctx) }

// StorageFixture is a fresh backend storage owned by one test case.
type StorageFixture interface {
	Storage() session.Storage
	Dispose() error
}

// ConformanceCase is a runner-independent test case.
type ConformanceCase struct {
	Group string
	Name  string
	Run   func() error
}

// InstrumentedStorage records commit admission transparently.
type InstrumentedStorage struct {
	StorageDecorator
	commitAttempts [][]session.Write
}

// NewInstrumentedStorage wraps a delegate.
func NewInstrumentedStorage(delegate session.Storage) *InstrumentedStorage {
	return &InstrumentedStorage{StorageDecorator: StorageDecorator{Delegate: delegate}}
}

// GetCommitAttempts returns recorded admissions (copy).
func (s *InstrumentedStorage) GetCommitAttempts() [][]session.Write {
	out := make([][]session.Write, len(s.commitAttempts))
	copy(out, s.commitAttempts)
	return out
}

// ClearCommitAttempts resets the recording.
func (s *InstrumentedStorage) ClearCommitAttempts() { s.commitAttempts = nil }

// Commit records then forwards.
func (s *InstrumentedStorage) Commit(writes []session.Write, ctx harness.Context) (session.CommitResult, error) {
	s.commitAttempts = append(s.commitAttempts, writes)
	return s.Delegate.Commit(writes, ctx)
}

// CommitDiscarded is thrown for every commit rejected after simulated
// storage loss.
type CommitDiscarded struct{ Message string }

func (e *CommitDiscarded) Error() string { return e.Message }

type parkedCommit struct {
	release chan struct{}
	drop    chan error
	landing chan error
}

// GatingStorage deterministically parks admitted commits.
type GatingStorage struct {
	StorageDecorator
	armed     bool
	discarded bool
	queue     []*parkedCommit
}

// NewGatingStorage wraps a delegate.
func NewGatingStorage(delegate session.Storage) *GatingStorage {
	return &GatingStorage{StorageDecorator: StorageDecorator{Delegate: delegate}}
}

// Arm enables gating (fixture setup bypasses until armed).
func (s *GatingStorage) Arm() { s.armed = true }

// Pending returns the parked count.
func (s *GatingStorage) Pending() int { return len(s.queue) }

// WaitPending blocks until at least count commits are parked.
func (s *GatingStorage) WaitPending(count int) error {
	if count < 1 {
		return fmt.Errorf("Pending commit count must be a positive safe integer")
	}
	if s.discarded {
		return &CommitDiscarded{Message: "storage discarded"}
	}
	if len(s.queue) >= count {
		return nil
	}
	// Poll until enough commits park (or a discard lands).
	for {
		if s.discarded {
			return &CommitDiscarded{Message: "storage discarded"}
		}
		if len(s.queue) >= count {
			return nil
		}
		// The parked channels are released by Next; polling keeps the
		// single-consumer test flow deterministic.
		waitTick()
	}
}

// Commit parks when armed, then forwards.
func (s *GatingStorage) Commit(writes []session.Write, ctx harness.Context) (session.CommitResult, error) {
	if s.discarded {
		return session.CommitResult{}, &CommitDiscarded{Message: "commit rejected: storage discarded"}
	}
	if !s.armed {
		return s.Delegate.Commit(writes, ctx)
	}
	parked := &parkedCommit{
		release: make(chan struct{}),
		drop:    make(chan error, 1),
		landing: make(chan error, 1),
	}
	s.queue = append(s.queue, parked)
	select {
	case <-parked.release:
		if s.discarded {
			return session.CommitResult{}, &CommitDiscarded{Message: "commit rejected: storage discarded"}
		}
		result, err := s.Delegate.Commit(writes, ctx)
		if err != nil {
			parked.landing <- err
		} else {
			parked.landing <- nil
		}
		return result, err
	case err := <-parked.drop:
		return session.CommitResult{}, err
	}
}

// Next releases count commits in FIFO order and waits until each write
// lands.
func (s *GatingStorage) Next(count int) error {
	if count < 1 {
		return fmt.Errorf("Released commit count must be a positive safe integer")
	}
	for index := 0; index < count; index++ {
		if err := s.WaitPending(1); err != nil {
			return err
		}
		if len(s.queue) == 0 {
			return fmt.Errorf("No parked commit")
		}
		parked := s.queue[0]
		s.queue = s.queue[1:]
		close(parked.release)
		if err := <-parked.landing; err != nil {
			return err
		}
	}
	return nil
}

// Discard drops parked commits and permanently rejects later ones.
func (s *GatingStorage) Discard() {
	if s.discarded {
		return
	}
	s.discarded = true
	err := &CommitDiscarded{Message: "commit discarded"}
	for _, parked := range s.queue {
		parked.drop <- err
	}
	s.queue = nil
}
