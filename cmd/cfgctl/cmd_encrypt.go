package main

import (
	"errors"
	"fmt"

	"github.com/spf13/pflag"

	"github.com/elmhuangyu/configs/pkg/syncer"
)

func runEncrypt(args []string) error {
	fs := pflag.NewFlagSet("encrypt", pflag.ContinueOnError)

	// SOPS master-key flags, forwarded verbatim to the sops binary. Each
	// accepts a comma separated list, mirroring sops itself.
	var keys syncer.KeyOptions
	fs.StringVar(&keys.GCPKMS, "gcp-kms", "", "comma separated GCP Cloud KMS resource IDs")
	fs.StringVar(&keys.KMS, "kms", "", "comma separated AWS KMS ARNs")
	fs.StringVar(&keys.AzureKV, "azure-kv", "", "comma separated Azure Key Vault key URLs")
	fs.StringVar(&keys.HuaweiKMS, "hckms", "", "comma separated HuaweiCloud KMS key IDs")
	fs.StringVar(&keys.HCVaultTransit, "hc-vault-transit", "", "comma separated HashiCorp Vault transit key URIs")
	fs.StringVar(&keys.PGP, "pgp", "", "comma separated PGP fingerprints")
	fs.StringVar(&keys.Age, "age", "", "comma separated age recipients")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cfgctl encrypt <dir> [sops key flags]")
	}

	return syncer.EncryptDirectory(syncer.EncryptOptions{
		Dir:  fs.Arg(0),
		Keys: keys,
	})
}
