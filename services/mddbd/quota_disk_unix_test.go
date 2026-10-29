//go:build unix

package main

import (
	"context"
	"syscall"
	"testing"

	pb "mddb/proto"
)

// GO-043: the disk reserve end to end through the real diskSpace, with only
// the statfs system call standing in for a nearly full filesystem.
func TestDiskReserveWithInjectedStatfs(t *testing.T) {
	t.Setenv("MDDB_DISK_RESERVE_BYTES", "4096")
	gs, s := quotaTestServer(t)
	orig := statfs
	statfs = func(_ string, st *syscall.Statfs_t) error {
		st.Bsize = 512
		st.Blocks = 1 << 20
		st.Bavail = 4 // 2 KiB available, under the 4 KiB reserve
		return nil
	}
	t.Cleanup(func() { statfs = orig })

	_, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"})
	wantGRPCQuota(t, err)
	if n := quotaDocCount(t, s, "q"); n != 2 {
		t.Fatalf("%d documents stored, want 2", n)
	}
	if _, err := gs.Get(context.Background(), &pb.GetRequest{Collection: "q", Key: "a", Lang: "en"}); err != nil {
		t.Fatalf("read refused below the reserve: %v", err)
	}
}
