package configlib

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// watchRetryDelay is the delay between watch re-establishment attempts after
// the server cancels the stream (for example following a compaction).
const watchRetryDelay = 10 * time.Second

// etcdKV is the subset of the etcd client used by EtcdSource. It exists so the
// watch retry loop can be exercised without a live cluster.
type etcdKV interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan
}

// EtcdSource reads configuration from an etcd v3 cluster under a single prefix.
type EtcdSource struct {
	client     etcdKV
	prefix     string
	retryDelay time.Duration
	onError    func(error)
	// lastRev is the revision of the most recent Fetch. Watching resumes from
	// lastRev+1 so no change is lost between a fetch and the watch it feeds.
	lastRev atomic.Int64
}

// NewEtcdSource creates an EtcdSource. The prefix is normalised to end with a
// trailing slash so relative keys never carry a leading slash. The source does
// not take ownership of client; the caller remains responsible for closing it.
func NewEtcdSource(client *clientv3.Client, prefix string) *EtcdSource {
	return newEtcdSource(client, prefix)
}

func newEtcdSource(client etcdKV, prefix string) *EtcdSource {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &EtcdSource{client: client, prefix: prefix, retryDelay: watchRetryDelay}
}

// OnError registers a callback for asynchronous watch/fetch failures that the
// reconnect loop recovers from. Set it before Start.
func (s *EtcdSource) OnError(fn func(error)) {
	s.onError = fn
}

func (s *EtcdSource) reportError(err error) {
	if s.onError != nil {
		s.onError(err)
	}
}

// Fetch reads all keys under the prefix in a single range request. It records
// the read revision so the subsequent Watch can resume exactly after it.
func (s *EtcdSource) Fetch(ctx context.Context) (*Snapshot, error) {
	resp, err := s.client.Get(ctx, s.prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	kvs := make(FlattenKVs, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		relPath := strings.TrimPrefix(string(kv.Key), s.prefix)
		kvs[relPath] = kv.Value
	}

	s.lastRev.Store(resp.Header.Revision)

	return &Snapshot{
		KVs:      kvs,
		Revision: resp.Header.Revision,
	}, nil
}

// Watch observes the prefix and emits a full snapshot on every change. The
// watch resumes from the revision of the most recent Fetch (see Fetch), so
// changes that land between the initial fetch and the watch are not missed.
// When the server cancels the stream (for example after a compaction, or a
// member restart) the watch is re-established after retryDelay. On every
// reconnect a fresh snapshot is emitted so the consumer catches up on changes
// that happened while it was disconnected. Recoverable failures are surfaced
// through OnError, so the watch never goes silently stale.
func (s *EtcdSource) Watch(ctx context.Context) (<-chan *Snapshot, error) {
	out := make(chan *Snapshot)

	go func() {
		defer close(out)

		// The manager has already fetched the initial state, so the first
		// session starts without an extra snapshot; only reconnect iterations
		// emit a catch-up snapshot.
		reconnected := false
		for {
			if reconnected {
				snap, err := s.Fetch(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					s.reportError(fmt.Errorf("etcd fetch after reconnect: %w", err))
					if !wait(ctx, s.retryDelay) {
						return
					}
					continue
				}
				if !emit(ctx, out, snap) {
					return
				}
			}

			reason := s.session(ctx, out)
			if ctx.Err() != nil {
				return
			}
			s.reportError(fmt.Errorf(
				"etcd watch on prefix %q ended (%s); reconnecting in %s",
				s.prefix, reason, s.retryDelay,
			))
			if !wait(ctx, s.retryDelay) {
				return
			}
			reconnected = true
		}
	}()

	return out, nil
}

// session consumes a single watch stream, emitting a snapshot for every event,
// and returns a human-readable reason once the stream ends. When a prior Fetch
// recorded a revision, the watch resumes from the next revision (WithRev), so
// changes that landed between the fetch and the watch are not missed.
func (s *EtcdSource) session(ctx context.Context, out chan<- *Snapshot) string {
	opts := []clientv3.OpOption{clientv3.WithPrefix()}
	if rev := s.lastRev.Load(); rev > 0 {
		opts = append(opts, clientv3.WithRev(rev+1))
	}

	rch := s.client.Watch(ctx, s.prefix, opts...)
	for wresp := range rch {
		if wresp.Canceled {
			if err := wresp.Err(); err != nil {
				return err.Error()
			}
			return "watch canceled"
		}

		snap, err := s.Fetch(ctx)
		if err != nil {
			s.reportError(fmt.Errorf("etcd fetch after event: %w", err))
			continue
		}
		if !emit(ctx, out, snap) {
			return "context canceled"
		}
	}
	return "stream closed"
}

// Close is a no-op. The etcd client is typically shared with the rest of the
// application and owned by the caller, so the source must not close it.
func (s *EtcdSource) Close() error {
	return nil
}

// wait blocks for d unless ctx is done first, reporting whether the delay
// elapsed.
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// emit sends snap on out unless ctx is done first.
func emit(ctx context.Context, out chan<- *Snapshot, snap *Snapshot) bool {
	select {
	case out <- snap:
		return true
	case <-ctx.Done():
		return false
	}
}

var _ Source = (*EtcdSource)(nil)
