package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "encrypt":
		err = runEncrypt(os.Args[2:])
	case "push":
		err = runPush(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "cfgctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cfgctl - SOPS-backed configuration publisher

Usage:
  cfgctl encrypt <dir> [sops key flags]
  cfgctl push <dir> --prefix <etcd_prefix> [--endpoints <host:port,...>] [--prune]

Commands:
  encrypt   Encrypt a local configuration directory with SOPS.
  push      Publish a directory to etcd in a single atomic transaction.

SOPS key flags for encrypt (comma separated, forwarded to sops):
  --gcp-kms <ids>          GCP Cloud KMS resource IDs
  --kms <arns>             AWS KMS ARNs
  --azure-kv <urls>        Azure Key Vault key URLs
  --hckms <ids>            HuaweiCloud KMS key IDs
  --hc-vault-transit <uris>
                           HashiCorp Vault transit key URIs
  --pgp <fingerprints>     PGP fingerprints
  --age <recipients>       age recipients

Examples:
  cfgctl encrypt ./config --gcp-kms projects/p/locations/us-central1/keyRings/sops/cryptoKeys/sops-key
  cfgctl push ./config --prefix /myapp/config --prune
`)
}
