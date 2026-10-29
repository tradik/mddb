package main

// Storing what a conversion made (GO-046): one file can be several documents
// — a mailbox's messages, an email's attachments — and every surface stores
// them the same way, through convertedDocs.

// convertedDoc is one document to store from a conversion.
type convertedDoc struct {
	Key       string
	Markdown  string
	Meta      map[string][]string
	Format    string
	Converted bool
	// Confidence is meaningful when Converted.
	Confidence float64
	Warnings   []string
	// Image is set for an image document: its bytes are stored apart from
	// it (RAG-010).
	Image *extractedImage
}

// convertedDocs lists the documents a conversion of the file stored under
// key yields: the file's own first, then its parts, then its images under
// KEY/images/ (RAG-010). Each gets a copy of base, the conversion's score and
// the file's own meta (base wins over the file's meta, so a caller can always
// override); parts and images also get meta.parent.
func convertedDocs(c conversion, key string, base map[string][]string) []convertedDoc {
	docs := []convertedDoc{convertedDocOf(c, key, base)}
	if c.Image != nil {
		docs[0] = imageDocOf(*c.Image, key, "", base)
	}
	if len(c.ImageWarnings) > 0 {
		docs[0].Meta["imageWarnings"] = c.ImageWarnings
	}
	for _, p := range c.Parts {
		d := convertedDocOf(p.conversion, key+p.Suffix, base)
		d.Meta["parent"] = []string{key + p.ParentSuffix}
		docs = append(docs, d)
	}
	for _, im := range c.Images {
		docs = append(docs, imageDocOf(im, key+"/images/"+im.Name, key, base))
	}
	return docs
}

// convertedDocOf is one conversion as a document under key.
func convertedDocOf(c conversion, key string, base map[string][]string) convertedDoc {
	meta := make(map[string][]string, len(base)+len(c.Meta)+2)
	for k, v := range base {
		meta[k] = v
	}
	conversionMeta(meta, c)
	return convertedDoc{Key: key, Markdown: c.Markdown, Meta: meta, Format: c.Format, Converted: c.Converted,
		Confidence: c.Confidence, Warnings: c.Warnings}
}
