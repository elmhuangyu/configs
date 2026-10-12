package configlib

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// fakeStream is a single fake watch stream. close is idempotent so it can be
// triggered by both context cancellation and an explicit server cancel.
type fakeStream struct {
	ch   chan clientv3.WatchResponse
	once sync.Once
}

func (s *fakeStream) close() {
	s.once.Do(func() { close(s.ch) })
}

// fakeEtcd is a minimal in-memory etcdKV used to exercise EtcdSource without a
// live cluster.
type fakeEtcd struct {
	mu       sync.Mutex
	kvs      map[string][]byte
	revision int64
	streams  []*fakeStream
}

func newFakeEtcd() *fakeEtcd {
	return &fakeEtcd{kvs: make(map[string][]byte)}
}

func (f *fakeEtcd) put(key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kvs[key] = []byte(value)
	f.revision++
}

func (f *fakeEtcd) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams)
}

// notify pushes an empty (non-canceled) response, which makes the source
// re-fetch and emit.
func (f *fakeEtcd) notify() {
	f.mu.Lock()
	streams := append([]*fakeStream(nil), f.streams...)
	f.mu.Unlock()
	for _, s := range streams {
		s.ch <- clientv3.WatchResponse{}
	}
}

// cancelStreams simulates a server-side cancellation (e.g. compaction).
func (f *fakeEtcd) cancelStreams() {
	f.mu.Lock()
	streams := f.streams
	f.streams = nil
	f.mu.Unlock()
	for _, s := range streams {
		s.ch <- clientv3.WatchResponse{Canceled: true, CancelReason: "compacted"}
		s.close()
	}
}

func (f *fakeEtcd) Get(_ context.Context, prefix string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	resp := &clientv3.GetResponse{Header: &pb.ResponseHeader{Revision: f.revision}}
	for k, v := range f.kvs {
		if strings.HasPrefix(k, prefix) {
			resp.Kvs = append(resp.Kvs, &mvccpb.KeyValue{Key: []byte(k), Value: v})
		}
	}
	sort.Slice(resp.Kvs, func(i, j int) bool {
		return string(resp.Kvs[i].Key) < string(resp.Kvs[j].Key)
	})
	return resp, nil
}

func (f *fakeEtcd) Watch(ctx context.Context, _ string, _ ...clientv3.OpOption) clientv3.WatchChan {
	s := &fakeStream{ch: make(chan clientv3.WatchResponse, 8)}
	f.mu.Lock()
	f.streams = append(f.streams, s)
	f.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.close()
	}()
	return s.ch
}

func (f *fakeEtcd) Close() error { return nil }

func TestEtcdSourceFetch(t *testing.T) {
	fake := newFakeEtcd()
	fake.put("/cfg/mode", "prod")
	fake.put("/cfg/agents/a/prompt.txt", "hi")
	fake.put("/other/key", "ignored")

	src := newEtcdSource(fake, "/cfg")
	snap, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(t, FlattenKVs{
		"mode":                []byte("prod"),
		"agents/a/prompt.txt": []byte("hi"),
	}, snap.KVs)
	assert.Equal(t, int64(3), snap.Revision)
}

func TestEtcdSourceWatchEmitsOnEvent(t *testing.T) {
	fake := newFakeEtcd()
	fake.put("/cfg/mode", "prod")

	src := newEtcdSource(fake, "/cfg")
	src.retryDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := src.Watch(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return fake.streamCount() == 1 }, time.Second, time.Millisecond)

	fake.put("/cfg/mode", "staging")
	fake.notify()

	select {
	case snap := <-events:
		assert.Equal(t, []byte("staging"), snap.KVs["mode"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}

func TestEtcdSourceWatchReconnectsAfterCancel(t *testing.T) {
	fake := newFakeEtcd()
	fake.put("/cfg/mode", "prod")

	src := newEtcdSource(fake, "/cfg")
	src.retryDelay = 5 * time.Millisecond

	var mu sync.Mutex
	var errs []error
	src.OnError(func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := src.Watch(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return fake.streamCount() == 1 }, time.Second, time.Millisecond)

	// The server cancels the stream; the source must reconnect and emit a
	// catch-up snapshot rather than going silently stale.
	fake.put("/cfg/mode", "staging")
	fake.cancelStreams()

	select {
	case snap := <-events:
		assert.Equal(t, []byte("staging"), snap.KVs["mode"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect snapshot")
	}

	require.Eventually(t, func() bool { return fake.streamCount() == 1 }, time.Second, time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "reconnecting")
}
