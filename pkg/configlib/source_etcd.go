package configlib

import (
	"context"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdSource reads configuration from an etcd v3 cluster under a single prefix.
type EtcdSource struct {
	client *clientv3.Client
	prefix string
}

// NewEtcdSource creates an EtcdSource. The prefix is normalised to end with a
// trailing slash so relative keys never carry a leading slash.
func NewEtcdSource(client *clientv3.Client, prefix string) *EtcdSource {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &EtcdSource{client: client, prefix: prefix}
}

// Fetch reads all keys under the prefix in a single range request.
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

	return &Snapshot{
		KVs:      kvs,
		Revision: resp.Header.Revision,
	}, nil
}

// Watch observes the prefix and emits a full snapshot on every change. Reusing
// Fetch keeps the snapshot consistent with the initial load while the
// per-event revision comes from the backend header.
func (s *EtcdSource) Watch(ctx context.Context) (<-chan *Snapshot, error) {
	out := make(chan *Snapshot)
	rch := s.client.Watch(ctx, s.prefix, clientv3.WithPrefix())

	go func() {
		defer close(out)
		for wresp := range rch {
			if wresp.Canceled {
				return
			}
			snap, err := s.Fetch(ctx)
			if err == nil {
				out <- snap
			}
		}
	}()

	return out, nil
}

// Close closes the underlying etcd client.
func (s *EtcdSource) Close() error {
	return s.client.Close()
}

var _ Source = (*EtcdSource)(nil)
