package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Reading the zip containers of OOXML (xlsx, pptx), OpenDocument (ods, odp)
// and EPUB, with a bound on what any one entry may inflate to.

// maxZipEntry bounds one entry: a sheet or slide of a legitimate file is far
// smaller, and a zip bomb is not. A variable only so a test can lower it.
var maxZipEntry int64 = 64 << 20

// zipDoc is an opened container: its entries by name.
type zipDoc struct {
	format string
	files  map[string]*zip.File
}

// openZipDoc opens data as a zip container of the named format.
func openZipDoc(data []byte, format string) (*zipDoc, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid %s file: %w", format, err)
	}
	z := &zipDoc{format: format, files: make(map[string]*zip.File, len(r.File))}
	for _, f := range r.File {
		z.files[f.Name] = f
	}
	return z, nil
}

// has reports whether the container holds the named entry.
func (z *zipDoc) has(name string) bool {
	_, ok := z.files[name]
	return ok
}

// read returns an entry's bytes, or an error naming the format when it is
// missing, unreadable or larger than maxZipEntry.
func (z *zipDoc) read(name string) ([]byte, error) {
	f, ok := z.files[name]
	if !ok {
		return nil, errors.New("invalid " + z.format + ": " + name + " not found")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %s: %w", z.format, name, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxZipEntry+1))
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %s: %w", z.format, name, err)
	}
	if int64(len(data)) > maxZipEntry {
		return nil, fmt.Errorf("invalid %s: %s inflates past %d bytes", z.format, name, maxZipEntry)
	}
	return data, nil
}

// readXML reads an entry and decodes it into v.
func (z *zipDoc) readXML(name string, v any) error {
	data, err := z.read(name)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("invalid %s: %s: %w", z.format, name, err)
	}
	return nil
}

// ooxmlRels is an OOXML relationships part (_rels/*.rels).
type ooxmlRels struct {
	Rels []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

// relTargets reads the relationships of part (e.g. "xl/workbook.xml") and
// returns each ID's target as a container path. A missing rels part is no
// relationships.
func (z *zipDoc) relTargets(part string) (map[string]string, map[string]string) {
	byID, byType := map[string]string{}, map[string]string{}
	var rels ooxmlRels
	relsPath := path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
	if z.readXML(relsPath, &rels) != nil {
		return byID, byType
	}
	for _, r := range rels.Rels {
		target := r.Target
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(path.Dir(part), target)
		}
		byID[r.ID] = target
		byType[path.Base(r.Type)] = target
	}
	return byID, byType
}
