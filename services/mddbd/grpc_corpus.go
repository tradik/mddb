package main

import (
	"context"
	"errors"
	"fmt"

	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Corpus packs over gRPC (AGT-005, #378). Each method calls what the REST
// handler calls — corpusPacks, installCorpus (or installCorpusFromURL),
// removeCorpus, prepareCorpus — and the access gate applies the REST
// permissions (access_rules_grpc.go): listing reads the caller's tenant,
// the rest need admin.

// corpusChunkBytes is the most one CorpusPackChunk carries, well under
// gRPC's default 4 MiB message limit.
const corpusChunkBytes = 1 << 20

// ListCorpusPacks lists the installed packs, as GET /v1/corpus.
func (g *GRPCServer) ListCorpusPacks(ctx context.Context, _ *proto.ListCorpusPacksRequest) (*proto.ListCorpusPacksResponse, error) {
	packs := g.server.corpusPacks(ctx)
	out := &proto.ListCorpusPacksResponse{Packs: make([]*proto.CorpusPackProto, 0, len(packs)), Total: safeInt32(len(packs))}
	for _, p := range packs {
		out.Packs = append(out.Packs, &proto.CorpusPackProto{Collection: p.Collection, ReadOnly: p.ReadOnly, Corpus: corpusInfoToProto(p.Corpus)})
	}
	return out, nil
}

// InstallCorpusPack installs a pack streamed by the client, or fetched from
// the https URL in the first message, as POST /v1/corpus/install.
func (g *GRPCServer) InstallCorpusPack(stream proto.MDDB_InstallCorpusPackServer) error {
	ctx := stream.Context()
	if err := g.corpusAdmin(ctx); err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "no pack was sent: "+err.Error())
	}
	var res *corpusInstallResult
	switch {
	case first.GetUrl() != "" && len(first.GetData()) > 0:
		return status.Error(codes.InvalidArgument, "send a url or the pack's data, not both")
	case first.GetUrl() != "":
		res, err = g.server.installCorpusFromURL(ctx, first.GetUrl(), first.GetSha256())
	default:
		res, err = g.server.installCorpusChecked(&corpusStreamReader{stream: stream, buf: first.GetData()}, first.GetSha256())
	}
	if err != nil {
		return corpusGRPCError(err)
	}
	return stream.SendAndClose(&proto.InstallCorpusPackResponse{Name: res.Name, Version: res.Version, License: res.License,
		DocCount: int64(res.DocCount), VectorsDisabled: res.VectorsDisabled, Warnings: res.Warnings})
}

// RemoveCorpusPack removes an installed pack, as POST /v1/corpus/remove.
func (g *GRPCServer) RemoveCorpusPack(ctx context.Context, req *proto.RemoveCorpusPackRequest) (*proto.RemoveCorpusPackResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := g.server.removeCorpus(req.GetName())
	if err != nil {
		return nil, corpusGRPCError(err)
	}
	return &proto.RemoveCorpusPackResponse{Status: "removed", Name: req.GetName(), Deleted: int64(n)}, nil
}

// BuildCorpusPack streams the pack of a collection, as POST /v1/corpus/build.
// Building reads only, so a read-only server builds too.
func (g *GRPCServer) BuildCorpusPack(req *proto.BuildCorpusPackRequest, stream proto.MDDB_BuildCorpusPackServer) error {
	if err := g.requireAdminRPC(stream.Context()); err != nil {
		return err
	}
	b, err := g.server.prepareCorpus(corpusBuildFromProto(req))
	if err != nil {
		return corpusGRPCError(err)
	}
	defer b.close()
	name := b.manifest.Name
	if b.manifest.Version != "" {
		name += "-" + b.manifest.Version
	}
	w := &corpusChunkWriter{stream: stream, first: &proto.CorpusPackChunk{Filename: name + ".mddbpack", DocCount: int64(b.manifest.DocCount)}}
	// A pack is never empty (a tar.zst holds at least its manifest), so the
	// first chunk always carries the file name.
	if err := b.writeTo(w); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

// corpusBuildFromProto is the REST body of a build request.
func corpusBuildFromProto(req *proto.BuildCorpusPackRequest) corpusBuildRequest {
	out := corpusBuildRequest{Collection: req.GetCollection(), Version: req.GetVersion(),
		Description: req.GetDescription(), License: req.GetLicense()}
	for _, src := range req.GetSources() {
		out.Sources = append(out.Sources, corpusSource{Repo: src.GetRepo(), Commit: src.GetCommit(), License: src.GetLicense()})
	}
	return out
}

// corpusAdmin admits a pack install or removal: a writable server and an
// administrator.
func (g *GRPCServer) corpusAdmin(ctx context.Context) error {
	if err := g.checkWritable(); err != nil {
		return err
	}
	return g.requireAdminRPC(ctx)
}

// requireAdminRPC refuses a caller who is not a global admin.
func (g *GRPCServer) requireAdminRPC(ctx context.Context) error {
	if g.server.AuthManager == nil {
		return nil
	}
	if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return nil
}

// corpusGRPCError maps a corpus failure to its status, as corpusError maps
// it to an HTTP one.
func corpusGRPCError(err error) error {
	code := codes.Internal
	switch {
	case errors.Is(err, errCorpusConflict):
		code = codes.AlreadyExists
	case errors.Is(err, errNotFound):
		code = codes.NotFound
	case errors.Is(err, errCorpusInvalid):
		code = codes.InvalidArgument
	}
	return status.Error(code, err.Error())
}

// corpusStreamReader reads the data of an install stream's messages.
type corpusStreamReader struct {
	stream proto.MDDB_InstallCorpusPackServer
	buf    []byte
}

// Read implements io.Reader.
func (r *corpusStreamReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		msg, err := r.stream.Recv()
		if err != nil {
			return 0, err // io.EOF when the client closed its side
		}
		if msg.GetUrl() != "" {
			return 0, fmt.Errorf("%w: a url after the pack's data", errCorpusInvalid)
		}
		r.buf = msg.GetData()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// corpusChunkWriter sends what it is given as CorpusPackChunk messages of at
// most corpusChunkBytes. The first message also names the file; it is held
// until data arrives.
type corpusChunkWriter struct {
	stream proto.MDDB_BuildCorpusPackServer
	first  *proto.CorpusPackChunk
}

// Write implements io.Writer.
func (w *corpusChunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), corpusChunkBytes)
		msg := &proto.CorpusPackChunk{}
		if w.first != nil {
			msg, w.first = w.first, nil
		}
		msg.Data = append([]byte(nil), p[:n]...)
		if err := w.stream.Send(msg); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}
