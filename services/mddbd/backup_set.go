package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// backupSetFile is one file of a backup set: its path relative to the set's
// directory, with "/" separators, its sha256 and its size.
type backupSetFile struct {
	rel  string
	sum  string
	size int64
}

// backupSet is one backup on local disk as backupTo writes it: the main file,
// in the per-collection layout the collection files in "<name>.d/", and —
// once hashed — "<name>.sha256" listing every file's sum in sha256sum's
// format, so `sha256sum -c` checks a copy anywhere.
type backupSet struct {
	dir   string          // the directory holding it
	name  string          // the main file's base name
	files []backupSetFile // the main file first, the checksum file last
}

// sumFileName is the name of a backup's checksum file.
func sumFileName(name string) string { return name + ".sha256" }

// hashBackupSet reads back every file of the backup at dir/name, records its
// sha256 and writes the checksum file beside it. Everything goes through an
// os.Root on dir.
func hashBackupSet(dir, name string) (*backupSet, error) {
	root, err := os.OpenRoot(dir) // NOSONAR gosecurity:S2083 -- dir is a backup target's directory, confined by safeBackupPath
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	rels := []string{name}
	sub := name + ".d"
	// A file where the collection directory belongs is a damaged set. Asked
	// to list it, Linux says "not a directory" but Windows says "not found",
	// which would pass for a set without collection files; so it is checked.
	if info, err := root.Stat(sub); err == nil && !info.IsDir() { // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
		return nil, fmt.Errorf("%s is not a directory", sub)
	}
	entries, err := fs.ReadDir(root.FS(), sub) // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			rels = append(rels, path.Join(sub, e.Name()))
		}
	}

	set := &backupSet{dir: dir, name: name}
	var list strings.Builder
	for _, rel := range rels {
		file, err := hashFile(root, rel)
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", rel, err)
		}
		set.files = append(set.files, file)
		fmt.Fprintf(&list, "%s  %s\n", file.sum, rel)
	}
	listed := []byte(list.String())
	if err := root.WriteFile(sumFileName(name), listed, 0o600); err != nil { // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
		return nil, err
	}
	listSum := sha256.Sum256(listed)
	set.files = append(set.files, backupSetFile{rel: sumFileName(name),
		sum: hex.EncodeToString(listSum[:]), size: int64(len(listed))})
	return set, nil
}

// hashFile reads one file inside root and returns its sha256 and size.
func hashFile(root *os.Root, rel string) (backupSetFile, error) {
	f, err := root.Open(rel) // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
	if err != nil {
		return backupSetFile{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return backupSetFile{rel: rel, sum: hex.EncodeToString(h.Sum(nil)), size: n}, err
}

// sha256Hex hashes everything r yields.
func sha256Hex(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// removeBackupSet deletes the backup dir/name: its main file, collection
// files and checksum file. A part already gone is not an error.
func removeBackupSet(dir, name string) error {
	root, err := os.OpenRoot(dir) // NOSONAR gosecurity:S2083 -- as above
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	var errs []error
	for _, p := range []string{name, sumFileName(name)} {
		if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) { // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
			errs = append(errs, err)
		}
	}
	if err := root.RemoveAll(name + ".d"); err != nil { // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
