package automations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/vault"
)

// vaultFingerprint hashes the (path, content-hash) of every note under an
// optional vault-relative prefix ("" = whole vault). It is the change-gate
// signal: a stable fingerprint means nothing new, so no work (and no model
// call) is needed. Cheap and makes no model call.
func vaultFingerprint(ctx context.Context, v *vault.FS, prefix string) (string, error) {
	paths, err := v.List(ctx)
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		if prefix != "" && !strings.HasPrefix(p, prefix) {
			continue
		}
		n, err := v.Read(ctx, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%s\n", p, config.ContentHash(n.Body))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
