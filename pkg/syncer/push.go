package syncer

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/elmhuangyu/configs/pkg/keypath"
)

// PushOptions configures a push operation.
type PushOptions struct {
	// Dir is the local directory to publish.
	Dir string
	// Prefix is the etcd key prefix that receives the tree.
	Prefix string
	// Endpoints lists the etcd endpoints to dial.
	Endpoints []string
	// Prune removes remote keys that no longer exist locally.
	Prune bool
}

// PushResult summarises a push operation.
type PushResult struct {
	// Prefix is the normalised etcd key prefix that was written.
	Prefix string
	// Puts is the number of keys written.
	Puts int
	// Deletes is the number of keys removed (only with Prune).
	Deletes int
	// Revision is the etcd revision the transaction committed at, or 0 when
	// there was nothing to push.
	Revision int64
}

// PushDirectory dials etcd and publishes opts.Dir under opts.Prefix in a single
// atomic transaction.
func PushDirectory(ctx context.Context, opts PushOptions) (PushResult, error) {
	client, err := clientv3.New(clientv3.Config{Endpoints: opts.Endpoints})
	if err != nil {
		return PushResult{}, err
	}
	defer func() { _ = client.Close() }()

	return Push(ctx, client, opts)
}

// Push publishes opts.Dir under opts.Prefix using the supplied client. All puts
// and deletes are committed in one clientv3.Txn so the cluster advances to a
// single revision. It returns statistics rather than printing them.
func Push(ctx context.Context, client *clientv3.Client, opts PushOptions) (PushResult, error) {
	prefix := opts.Prefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	result := PushResult{Prefix: prefix}

	local, err := CollectLocalFiles(opts.Dir)
	if err != nil {
		return PushResult{}, err
	}

	// Sort for deterministic operation ordering, which also makes the command
	// output reproducible.
	keys := make([]string, 0, len(local))
	for rel := range local {
		keys = append(keys, rel)
	}
	sort.Strings(keys)

	ops := make([]clientv3.Op, 0, len(keys))
	for _, rel := range keys {
		ops = append(ops, clientv3.OpPut(prefix+rel, string(local[rel])))
		result.Puts++
	}

	if opts.Prune {
		resp, err := client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithKeysOnly())
		if err != nil {
			return PushResult{}, err
		}
		for _, kv := range resp.Kvs {
			remoteKey := string(kv.Key)
			rel := strings.TrimPrefix(remoteKey, prefix)
			if _, ok := local[rel]; !ok {
				ops = append(ops, clientv3.OpDelete(remoteKey))
				result.Deletes++
			}
		}
	}

	if len(ops) == 0 {
		return result, nil
	}

	txnResp, err := client.Txn(ctx).Then(ops...).Commit()
	if err != nil {
		return PushResult{}, fmt.Errorf("commit etcd txn: %w", err)
	}
	result.Revision = txnResp.Header.Revision

	return result, nil
}

// CollectLocalFiles flattens dir into logical keys. Ignore rules apply (see
// IgnoreFileName and the built-in excludes). Plain files shadowed by an
// encrypted raw envelope ("<name>.sops.json") are skipped, and the envelope is
// exposed under the original logical path.
func CollectLocalFiles(dir string) (map[string][]byte, error) {
	files := make(map[string][]byte)

	err := walkConfigFiles(dir, func(rel, full string) error {
		if _, statErr := os.Stat(full + keypath.SOPSRawSuffix); statErr == nil {
			return nil
		}

		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		files[keypath.LogicalKey(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
