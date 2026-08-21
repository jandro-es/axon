package vault

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyFile streams srcPath into the vault at destRel (FR-213, ADR-042).
//
// It exists because the image archive path holds a whole file in memory as a
// string (Vault.Create(path, string(bytes))) — fine for a screenshot,
// untenable for an hour of .wav. Binary attachments stream; text notes keep
// using Create.
//
// Copies, never moves: the owner's file stays where it is. Refuses to
// overwrite, so a content-hash collision can never silently replace an
// existing attachment. destRel goes through the same safeAbs/symlink guards
// as every other vault writer.
func (v *FS) CopyFile(destRel, srcPath string) error {
	abs, err := v.safeAbs(destRel)
	if err != nil {
		return err
	}
	if err := v.checkNoSymlinkEscape(abs); err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("copy into vault: source %q: %w", filepath.Base(srcPath), err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("copy into vault: create %q: %w", destRel, err)
	}
	// O_EXCL: refuse to overwrite an existing attachment.
	out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("copy into vault: destination %q: %w", destRel, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(abs)
		return fmt.Errorf("copy into vault: %q: %w", destRel, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(abs)
		return fmt.Errorf("copy into vault: close %q: %w", destRel, err)
	}
	return nil
}
