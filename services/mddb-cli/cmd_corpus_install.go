package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Installing a pack (AGT-005). The server takes a pack only as an upload, so
// a pack named by URL is downloaded here — over TLS only, its checksum
// checked when --sha256 is given — and then uploaded.

// corpusHTTPClient fetches packs. A redirect may not leave TLS.
var corpusHTTPClient = &http.Client{CheckRedirect: func(req *http.Request, _ []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing a redirect to %s: packs are fetched over https only", req.URL.Scheme)
	}
	return nil
}}

func newCorpusInstallCmd() *cobra.Command {
	var wantSum string
	cmd := &cobra.Command{
		Use:   "install <file.mddbpack|https://...>",
		Short: "Install a pack as a read-only collection",
		Long: `Upload a pack to the server, which installs it under the collection name in
its manifest, read-only, without a restart. A pack named by an https:// URL is
downloaded first. --sha256 checks the pack file before anything is sent; the
server checks collection.db against the manifest's checksum either way.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, cleanup, err := localPack(args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			if wantSum != "" {
				if err := checkFileSum(path, wantSum); err != nil {
					return err
				}
			}
			raw, err := uploadPack(path)
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(raw))
				return nil
			}
			var res struct {
				Name     string   `json:"name"`
				Version  string   `json:"version"`
				DocCount int      `json:"docCount"`
				License  string   `json:"license"`
				Warnings []string `json:"warnings"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			fmt.Printf("✓ Installed pack %s %s: %d documents, licence %s (read-only)\n", res.Name, res.Version, res.DocCount, res.License)
			for _, w := range res.Warnings {
				fmt.Printf("! %s\n", w)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&wantSum, "sha256", "", "Expected sha256 of the pack file")
	return cmd
}

// localPack is the pack as a local file: arg itself, or arg downloaded when
// it is a URL. cleanup removes a download.
func localPack(arg string) (string, func(), error) {
	noop := func() {
		// Nothing to remove: a local file is the user's, and a failed
		// download has already removed its temporary file.
	}
	if !strings.Contains(arg, "://") {
		return arg, noop, nil
	}
	if !strings.HasPrefix(arg, "https://") {
		return "", noop, fmt.Errorf("%s: packs are fetched over https only", arg)
	}
	f, err := os.CreateTemp("", "mddb-pack-*.mddbpack")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	err = download(arg, f)
	if err = errors.Join(err, f.Close()); err != nil {
		cleanup()
		return "", noop, err
	}
	return f.Name(), cleanup, nil
}

// download writes the body at url to w.
func download(url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mddb-cli/"+CurrentVersion())
	resp, err := corpusHTTPClient.Do(req) // #nosec G107 G704 -- the user's own URL, https only, fetched by the user's own CLI
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// checkFileSum compares the sha256 of the file at path with want.
func checkFileSum(path, want string) error {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- the pack the user named
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(want) {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s): nothing was installed", filepath.Base(path), want, got)
	}
	return nil
}

// uploadPack sends the pack at path to the server as multipart, streamed
// from disk, and returns the server's answer.
func uploadPack(path string) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- the pack the user named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("pack", filepath.Base(path))
		if err == nil {
			_, err = io.Copy(part, f)
		}
		_ = pw.CloseWithError(errors.Join(err, mw.Close()))
	}()
	rc, err := newClient().DoStream(context.Background(), "POST", "/v1/corpus/install", mw.FormDataContentType(), pr)
	if err != nil {
		_ = pr.Close() // unblocks the writer if the server answered early
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// saveStream writes r to path through a temporary file beside it, renamed
// into place once complete, and returns the bytes written and their sha256.
func saveStream(path string, r io.Reader) (int64, string, error) {
	path = filepath.Clean(path)
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	// Closed before the rename or the remove: Windows refuses both on an open file.
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
