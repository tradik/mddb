package main

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// Images with search results (RAG-010).
//
// includeImages on a search attaches each hit's image documents — those
// whose meta.parent is the hit's key, in its language — with their alt text,
// caption and page, as `images` on the result or passage. It is one lookup
// of the metadata index for every hit of a collection at once, so a page of
// results costs one read whatever its size. Without includeImages nothing is
// looked up and nothing is added: the response is what it was before images
// existed.

// ImageOptions is the images part of a search request.
type ImageOptions struct {
	// IncludeImages attaches each hit's images and, where the collection's
	// images are embedded, adds the images the query matches by their
	// picture (imageHits).
	IncludeImages bool `json:"includeImages,omitempty"`
}

// ImageRef is an image document as a search returns it.
type ImageRef struct {
	Key      string `json:"key"`
	Lang     string `json:"lang"`
	AltText  string `json:"altText,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Page     int    `json:"page,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	// Parent is the document the image came from.
	Parent string `json:"parent,omitempty"`
	// Score is how close the image's vector is to the query, on image hits.
	Score float32 `json:"score,omitempty"`
	// URL is where the image's bytes are served (GET /v1/image).
	URL string `json:"url"`
	// position orders a document's images as they appear in it.
	position int
}

// imageOwner is a document whose images are asked for.
type imageOwner struct{ collection, key, lang string }

// imageRefOf is an image document as a reference.
func imageRefOf(collection string, doc *storage.Doc) ImageRef {
	atoi := func(k string) int { n, _ := strconv.Atoi(first(doc.Meta[k])); return n }
	return ImageRef{
		Key: doc.Key, Lang: doc.Lang,
		AltText: first(doc.Meta[metaAltText]), Caption: first(doc.Meta[metaCaption]),
		Page: atoi(metaPage), MimeType: first(doc.Meta[metaMimeType]),
		Width: atoi(metaImageWidth), Height: atoi(metaImageHeight),
		Parent: first(doc.Meta[metaParent]), position: atoi(metaPosition),
		URL: "/v1/image?collection=" + url.QueryEscape(collection) + "&key=" + url.QueryEscape(doc.Key) +
			"&lang=" + url.QueryEscape(doc.Lang),
	}
}

// imagesFor returns the images of each owner, in the order they appear in
// their document.
func (s *Server) imagesFor(owners []imageOwner) map[imageOwner][]ImageRef {
	out := map[imageOwner][]ImageRef{}
	byColl := map[string][]imageOwner{}
	for _, o := range owners {
		byColl[o.collection] = append(byColl[o.collection], o)
	}
	for coll, list := range byColl {
		want := make(map[imageOwner]bool, len(list))
		keys := make([]string, 0, len(list))
		for _, o := range list {
			if !want[o] {
				want[o] = true
				keys = append(keys, o.key)
			}
		}
		_ = s.DBViewC(coll, func(tx *bolt.Tx) error {
			bDocs := tx.Bucket([]byte("docs"))
			ids := metaFilterIDs(tx, coll, mergeFilters(map[string][]string{metaKind: {kindImage}, metaParent: keys}, nil))
			for _, id := range sortedIDs(ids) {
				doc, err := loadDoc(bDocs.Get(storage.DocKey(coll, id)))
				if err != nil || doc == nil {
					continue
				}
				o := imageOwner{coll, first(doc.Meta[metaParent]), doc.Lang}
				if want[o] {
					out[o] = append(out[o], imageRefOf(coll, doc))
				}
			}
			return nil
		})
	}
	for o, refs := range out {
		sort.SliceStable(refs, func(i, j int) bool { return imageOrder(refs[i]) < imageOrder(refs[j]) })
		out[o] = refs
	}
	return out
}

// imageOrder sorts a document's images as they appear in it: by page, then
// position, then key.
func imageOrder(r ImageRef) string {
	return strconv.Itoa(1_000_000+r.Page) + "\x00" + strconv.Itoa(1_000_000+r.position) + "\x00" + r.Key
}

// attachImages sets each item's images: one lookup for all of them.
func attachImages[T any](s *Server, items []T, owner func(*T) imageOwner, set func(*T, []ImageRef)) {
	if len(items) == 0 {
		return
	}
	owners := make([]imageOwner, len(items))
	for i := range items {
		owners[i] = owner(&items[i])
	}
	images := s.imagesFor(owners)
	for i := range items {
		if refs := images[owners[i]]; len(refs) > 0 {
			set(&items[i], refs)
		}
	}
}

// attachPassageImages sets each passage's images.
func (s *Server) attachPassageImages(p *PassagesResponse) {
	attachImages(s, p.Passages,
		func(it *Passage) imageOwner { return imageOwner{it.Collection, it.Key, it.Lang} },
		func(it *Passage, refs []ImageRef) { it.Images = refs })
}

// handleImage serves an image document's bytes: GET
// /v1/image?collection=&key=&lang=. The type is the one the bytes have, never
// a stored claim, and the browser is told not to guess another.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	collection, key, lang := s.Aliases.Resolve(q.Get("collection")), q.Get("key"), q.Get("lang")
	if collection == "" || key == "" || lang == "" {
		http.Error(w, `{"error":"missing collection, key or lang"}`, http.StatusBadRequest)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	data := s.imageBytes(collection, genID(collection, key, lang))
	mime, _, _, err := validateImage(data)
	if data == nil || err != nil {
		http.Error(w, `{"error":"image not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}
