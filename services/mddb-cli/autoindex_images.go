package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// Images for autoindex --images (RAG-010).
//
// The server stores a Markdown file's images as image documents when the
// files its links name are sent with it; it never reads the client's disk.
// The CLI finds them: the relative image links of each Markdown file,
// resolved against the file's directory, kept only inside the indexed root,
// only JPEG, PNG, GIF and WebP, and at most 2 MB each — the server's own
// limits, so nothing is sent only to be refused.

// maxImageFile is the largest image the server keeps.
const maxImageFile = 2 << 20

// mdImageLinkRe finds the target of a Markdown image link.
var mdImageLinkRe = regexp.MustCompile(`!\[[^\]]*\]\(\s*(?:<([^>]+)>|([^\s)]+))`)

// imageFileExtensions are the image formats the server keeps.
var imageFileExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

// markdownImageFiles reads the images a Markdown file's relative links name.
// rel is the file's path under root.
func markdownImageFiles(root, rel string, content []byte) []mddb.IngestImage {
	var out []mddb.IngestImage
	seen := map[string]bool{}
	// A directory on the way may be a link out of the tree: the image's
	// real path must still be under the root's.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	for _, m := range mdImageLinkRe.FindAllStringSubmatch(string(content), -1) {
		target := strings.SplitN(m[1]+m[2], "?", 2)[0]
		if seen[target] || strings.Contains(target, ":") || strings.HasPrefix(target, "/") ||
			!imageFileExtensions[strings.ToLower(path.Ext(target))] {
			continue
		}
		seen[target] = true
		under := path.Clean(path.Join(path.Dir(rel), target))
		if under == ".." || strings.HasPrefix(under, "../") {
			continue // outside the directory being indexed
		}
		abs, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(under)))
		if err != nil || !strings.HasPrefix(abs, realRoot+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(abs) // #nosec G703 -- abs is resolved and inside the root (checked above)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageFile {
			continue
		}
		// A file that cannot be read is left out, like one that is missing.
		if data, err := os.ReadFile(abs); err == nil { // #nosec G304 G703 -- a file inside the directory the user asked to index (checked above)
			out = append(out, mddb.IngestImage{Path: target, Content: data})
		}
	}
	return out
}
