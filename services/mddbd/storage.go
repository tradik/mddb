package main

import (
	pb "mddb/proto"
	"google.golang.org/protobuf/proto"
)

// Marshal document to protobuf bytes for storage with optional compression
func marshalDoc(doc *Doc) ([]byte, error) {
	protoDoc := docToProtoInternal(doc)
	data, err := proto.Marshal(protoDoc)
	if err != nil {
		return nil, err
	}
	
	// Compress if beneficial
	return compressDoc(data), nil
}

// Unmarshal document from protobuf bytes with decompression support
func unmarshalDoc(data []byte) (*Doc, error) {
	// Decompress if needed
	decompressed, err := decompressDoc(data)
	if err != nil {
		return nil, err
	}
	
	protoDoc := &pb.Document{}
	if err := proto.Unmarshal(decompressed, protoDoc); err != nil {
		return nil, err
	}
	return protoToDoc(protoDoc), nil
}

// Convert internal Doc to proto Document
func docToProtoInternal(doc *Doc) *pb.Document {
	protoMeta := make(map[string]*pb.MetaValues)
	for k, v := range doc.Meta {
		protoMeta[k] = &pb.MetaValues{Values: v}
	}

	return &pb.Document{
		Id:        doc.ID,
		Key:       doc.Key,
		Lang:      doc.Lang,
		Meta:      protoMeta,
		ContentMd: doc.ContentMD,
		AddedAt:   doc.AddedAt,
		UpdatedAt: doc.UpdatedAt,
	}
}

// Convert proto Document to internal Doc
func protoToDoc(protoDoc *pb.Document) *Doc {
	meta := make(map[string][]string)
	for k, v := range protoDoc.Meta {
		meta[k] = v.Values
	}

	return &Doc{
		ID:        protoDoc.Id,
		Key:       protoDoc.Key,
		Lang:      protoDoc.Lang,
		Meta:      meta,
		ContentMD: protoDoc.ContentMd,
		AddedAt:   protoDoc.AddedAt,
		UpdatedAt: protoDoc.UpdatedAt,
	}
}
