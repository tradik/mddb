package main

import (
	"context"
	"path"
	"strings"
)

// Image documents from files (RAG-010): which writes extract images, what an
// image document is made of, and how its bytes are written with it.

// collectionTypeImages is the collection type whose writes extract images by
// default and which accepts image files of their own.
const collectionTypeImages = "images"

// imagesCollection reports whether a collection is of type "images".
func (s *Server) imagesCollection(collection string) bool {
	if s.CollectionManager == nil {
		return false
	}
	cfg, ok := s.CollectionManager.Get(collection)
	return ok && cfg.Type == collectionTypeImages
}

// imageOptionsFor is the image extraction a write gets: what it asked for,
// with extraction on by default in a collection of type "images". local
// finds files sent with the document.
func (s *Server) imageOptionsFor(collection string, extract, fetchRemote bool, local func(string) ([]byte, bool)) imageOptions {
	return imageOptions{Extract: extract || s.imagesCollection(collection), FetchRemote: fetchRemote, Local: local}
}

// localFiles finds a referenced file among files keyed by path: by its
// cleaned path, else by its base name, since a multipart upload sends base
// names only.
func localFiles(files map[string][]byte) func(string) ([]byte, bool) {
	if len(files) == 0 {
		return nil
	}
	byBase := make(map[string][]byte, len(files))
	clean := make(map[string][]byte, len(files))
	for p, data := range files {
		clean[path.Clean(strings.TrimPrefix(p, "./"))] = data
		byBase[path.Base(p)] = data
	}
	return func(ref string) ([]byte, bool) {
		if data, ok := clean[ref]; ok {
			return data, true
		}
		data, ok := byBase[path.Base(ref)]
		return data, ok
	}
}

// isImageFile reports whether a file is an image of a kept format, by its
// declared format or its signature.
func isImageFile(format string, data []byte) bool {
	if imageFormats[normalizeFormat(format)] != "" {
		return true
	}
	_, _, _, err := validateImage(data)
	return err == nil
}

// convertFile is convertToMarkdown for a write to a collection: an image file
// in a collection of type "images" becomes an image document of its own, and
// with extraction on a document's images become image documents under it.
func (s *Server) convertFile(ctx context.Context, collection, format string, data []byte, textOnly bool, opts imageOptions) (conversion, error) {
	if s.imagesCollection(collection) && isImageFile(format, data) {
		im, err := newExtractedImage(data, "", "", "")
		if err != nil {
			return conversion{Format: normalizeFormat(format)}, err
		}
		return conversion{Format: imageExtensions[im.MimeType], Image: &im}, nil
	}
	c, err := convertToMarkdown(format, data, textOnly)
	if err != nil || !opts.Extract {
		return c, err
	}
	c.Images, c.ImageWarnings = extractImages(ctx, c.Format, data, c.Markdown, opts)
	return c, nil
}

// imageDocOf is an image as the document stored under key: parent is the
// document it came from, "" for an image file stored on its own, whose alt
// text and caption come from the caller's meta.
func imageDocOf(im extractedImage, key, parent string, base map[string][]string) convertedDoc {
	if parent == "" {
		if im.AltText == "" {
			im.AltText = first(base[metaAltText])
		}
		if im.Caption == "" {
			im.Caption = first(base[metaCaption])
		}
		if im.Name == "" {
			im.Name = path.Base(key)
			if path.Ext(im.Name) == "" {
				im.Name += "." + imageExtensions[im.MimeType]
			}
		}
	}
	return convertedDoc{Key: key, Markdown: imageDocContent(im), Meta: imageDocMeta(base, im, parent),
		Format: imageExtensions[im.MimeType], Image: &im}
}

// withImageBytes writes an image document's bytes, then the document itself
// by write: the bytes first, so the embedding job the write queues finds
// them. When write fails, the bytes the document had before are put back.
func (s *Server) withImageBytes(collection, lang string, d convertedDoc, write func() error) error {
	if d.Image == nil {
		return write()
	}
	id := genID(collection, d.Key, lang)
	old := s.imageBytes(collection, id)
	if err := s.putImageBytes(collection, id, d.Image.Data); err != nil {
		return err
	}
	err := write()
	switch {
	case err == nil:
	case old != nil:
		_ = s.putImageBytes(collection, id, old)
	default:
		s.deleteImageBytes(collection, id)
	}
	return err
}
