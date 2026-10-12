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

// maxTxnBytes bounds the size of a single push transaction. etcd's default
// --max-request-bytes is 1.5 MiB and it rejects anything larger, so the guard
// sits just below that to turn an opaque server rejection into a clear local
// error. Atomicity is preserved: the payload is never split across revisions.
const maxTxnBytes = 1_500_000

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

	var remoteKeys []string
	if opts.Prune {
		resp, err := client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithKeysOnly())
		if err != nil {
			return PushResult{}, err
		}
		for _, kv := range resp.Kvs {
			remoteKeys = append(remoteKeys, string(kv.Key))
		}
	}

	ops, size := buildPushOps(prefix, local, remoteKeys)
	if err := checkTxnSize(size); err != nil {
		return PushResult{}, err
	}

	for _, op := range ops {
		if op.IsDelete() {
			result.Deletes++
		} else {
			result.Puts++
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

// buildPushOps renders the transaction for a push: a Put for every local file
// plus a Delete for every remote key that no longer exists locally. It returns
// the ops in deterministic order along with their total key+value size.
func buildPushOps(prefix string, local map[string][]byte, remoteKeys []string) ([]clientv3.Op, int) {
	rels := make([]string, 0, len(local))
	for rel := range local {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	ops := make([]clientv3.Op, 0, len(rels))
	total := 0
	for _, rel := range rels {
		key := prefix + rel
		value := local[rel]
		ops = append(ops, clientv3.OpPut(key, string(value)))
		total += len(key) + len(value)
	}

	for _, remoteKey := range remoteKeys {
		if _, ok := local[strings.TrimPrefix(remoteKey, prefix)]; ok {
			continue
		}
		ops = append(ops, clientv3.OpDelete(remoteKey))
		total += len(remoteKey)
	}

	return ops, total
}

// checkTxnSize rejects a transaction that would exceed etcd's request limit.
func checkTxnSize(total int) error {
	if total > maxTxnBytes {
		return fmt.Errorf(
			"configuration is %d bytes, exceeding the %d byte single-transaction limit; "+
				"reduce the payload, split it across prefixes, or raise etcd's --max-request-bytes",
			total, maxTxnBytes,
		)
	}
	return nil
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
