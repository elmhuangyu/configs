package syncer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// PushDirectory dials etcd and publishes opts.Dir under opts.Prefix in a single
// atomic transaction.
func PushDirectory(ctx context.Context, opts PushOptions) error {
	client, err := clientv3.New(clientv3.Config{Endpoints: opts.Endpoints})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return Push(ctx, client, opts)
}

// Push publishes opts.Dir under opts.Prefix using the supplied client. All puts
// and deletes are committed in one clientv3.Txn so the cluster advances to a
// single revision.
func Push(ctx context.Context, client *clientv3.Client, opts PushOptions) error {
	prefix := opts.Prefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	local, err := CollectLocalFiles(opts.Dir)
	if err != nil {
		return err
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
	}

	if opts.Prune {
		resp, err := client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithKeysOnly())
		if err != nil {
			return err
		}
		for _, kv := range resp.Kvs {
			remoteKey := string(kv.Key)
			rel := strings.TrimPrefix(remoteKey, prefix)
			if _, ok := local[rel]; !ok {
				ops = append(ops, clientv3.OpDelete(remoteKey))
			}
		}
	}

	if len(ops) == 0 {
		fmt.Printf("Nothing to push to %s\n", prefix)
		return nil
	}

	txnResp, err := client.Txn(ctx).Then(ops...).Commit()
	if err != nil {
		return fmt.Errorf("commit etcd txn: %w", err)
	}

	fmt.Printf("Pushed %d operations to %s at revision %d\n", len(ops), prefix, txnResp.Header.Revision)
	return nil
}

// CollectLocalFiles flattens dir into logical keys. Plain files shadowed by an
// encrypted raw envelope ("<name>.sops.json") are skipped, and the envelope is
// exposed under the original logical path.
func CollectLocalFiles(dir string) (map[string][]byte, error) {
	files := make(map[string][]byte)

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if _, statErr := os.Stat(p + keypath.SOPSRawSuffix); statErr == nil {
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[keypath.LogicalKey(filepath.ToSlash(rel))] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
