# Storage Tuning

How mddb uses the disk, and what to set on each platform so that starts, reads
and writes stay fast as a database grows. Numbers marked *measured* come from
mddb's own tests; everything else is a recommendation with its reason.

## What mddb asks of the disk

mddb stores everything in one bbolt file:

- **Reads go through mmap.** A page the process touches for the first time is
  a page fault and a 4 KiB random read. The page cache is the working set:
  once a page is cached, reading it costs nothing.
- **Every write transaction ends in an fsync.** A disk's flush latency, not its
  bandwidth, decides how many commits a second it can take
  ([DEPLOYMENT.md](DEPLOYMENT.md#spinning-disks-and-fsync)).
- **The file never shrinks.** Deleted pages are reused, not returned to the
  filesystem.
- **An open after a crash walks every page** to rebuild the freelist, one
  random read at a time. On a 101 GB file on a virtual disk that took 101
  minutes, at about 17 MB/s.

Since 2.18.2 a clean shutdown stores the freelist, so planned restarts open in
milliseconds. After a crash the server starts at once and serves reads while
the freelist is rebuilt in the background; writes wait for it (503 with
`Retry-After`). The rebuild reads the file sequentially into the cache first
if it fits in half the available memory, and `/health` reports progress and
an ETA.

| start, 2.5 GB file, WSL2 ext4, cache dropped (*measured*) | open |
|---|---|
| walk (2.18.1 behaviour) | 56–60 s |
| after a clean shutdown | 0.014 s |
| after a crash, sequential prewarm first | 1.5 s + 0.4 s |

With `MDDB_STORAGE_LAYOUT=per-collection` every collection has its own file,
so a deleted collection gives its space back at once and a large collection's
walk does not hold up writes to a small one ([STORAGE-LAYOUT.md](STORAGE-LAYOUT.md)).

## Rules that hold everywhere

1. **Let the server shut down.** SIGTERM, then wait: the shutdown has a 15 s
   budget (`MDDB_SHUTDOWN_TIMEOUT_SEC`) and stores the freelist at the end.
   Docker kills after 10 s by default, so set `stop_grace_period: 20s` (the
   bundled compose files do); Kubernetes waits 30 s by default.
2. **Keep `MDDB_FREELIST_SYNC` on for large files.** Since 2.18.2 it is on by
   default for a file of 4 GiB or more (`MDDB_FREELIST_SYNC_THRESHOLD`); set it
   to `true` for smaller ones where crashes happen. It protects the start
   after a crash, which the clean-shutdown store cannot. It costs a little per commit (0.15 → 0.66 ms on tmpfs,
   *measured*), less on a disk that already waits for fsync.
3. **Give it memory for the hot part of the file.** A container memory limit
   counts the page cache; a limit below the working set turns every read into
   a disk read.
4. **Use local storage.** NFS, SMB, 9p and Docker bind mounts through a VM
   (VirtioFS, gRPC FUSE) add latency to every page fault and do not give mmap
   and file locks the guarantees bbolt relies on.
5. **Do not disable write barriers or fsync** unless the database can be
   rebuilt from its sources.

## Disks

| Disk | Random 4 KiB read | What it means for mddb |
|---|---|---|
| Local NVMe | ~0.1 ms | A cold walk runs at hundreds of MB/s. The best choice. |
| SATA SSD | ~0.1–0.2 ms | Fine. |
| Cloud block (EBS gp3, Persistent Disk) | ~0.5–1 ms, capped by provisioned IOPS | gp3's baseline is 3,000 IOPS, about 12 MB/s at 4 KiB. Provision more IOPS for large databases, or use instance-local NVMe. |
| Virtual disk on a laptop (WSL2, Docker Desktop) | ~0.2 ms | About 17 MB/s for a walk, as above. |
| 7200 rpm disk | ~8 ms | A walk runs at 0.5 MB/s. Use freelist sync, and an SSD for anything that restarts. |

## Linux

- **Filesystem:** ext4 or XFS, mounted with `noatime`.
- **I/O scheduler:** `none` for NVMe; `mq-deadline` for SATA SSD and HDD.
- **Btrfs:** copy-on-write fragments a file rewritten in place page by page.
  Create the data directory with `chattr +C` before the database exists, so
  the file is not copy-on-write.
- **ZFS:** see [ZFS](#zfs).
- **Swap:** keep `vm.swappiness` low (10) on a dedicated server, so the
  kernel drops cold file pages rather than swapping the process out.
- **Containers:** the page cache counts against the cgroup's `memory.max`.
  mddb reads that limit when it decides whether to prewarm.

## WSL2

- **Keep the database inside the Linux filesystem** (`/home/...`, the ext4
  VHDX), never under `/mnt/c`. `/mnt/c` goes through 9p, which is far slower
  for random reads and cannot give mmap coherent locking.
- **`.wslconfig` (`%UserProfile%\.wslconfig`):**

  ```ini
  [wsl2]
  memory=24GB                # the page cache lives inside the VM's memory
  [experimental]
  autoMemoryReclaim=disabled # "gradual"/"dropCache" empty the page cache when idle
  sparseVhd=true             # give space back to Windows after deletes
  ```

  With automatic reclaim on, an idle server loses its cache and the next
  queries read from disk again.
- **Windows Defender:** exclude the distribution's `ext4.vhdx` from real-time
  scanning; every write to the database is a write to that file.
- **Docker Desktop:** use a named volume, not a bind mount from Windows.

## macOS

- **Commits are slower than on Linux, by design.** Go's `fsync` on macOS is
  `F_FULLFSYNC`, which waits until the drive has written its cache to the
  media; plain `fsync` on macOS does not. Expect milliseconds per commit, and
  load in batches (`add-batch`, `ingest`) rather than one document per request.
- **Spotlight:** exclude the data directory (System Settings → Spotlight →
  Privacy, or `touch <dir>/.metadata_never_index`). Indexing a file that
  changes on every commit costs CPU and I/O for nothing.
- **Time Machine:** exclude the database (`tmutil addexclusion <file>`). Every
  commit dirties pages, so each hourly backup copies the whole file again, and
  a copy taken mid-write is not a consistent backup. Back up with
  `GET /v1/backup` ([BACKUP.md](BACKUP.md)).
- **Docker Desktop:** use a named volume; VirtioFS bind mounts add latency to
  every page fault.

## FreeBSD

- **UFS:** soft updates with journaling (the default), `noatime`.
- **ZFS:** see below. ZFS is the common choice and needs the most attention.

## ZFS

On Linux and FreeBSD alike:

- **`recordsize=16K`** on the dataset holding the database (set it before
  creating the file). With the default 128K, every 4 KiB page fault reads and
  checksums a 128K record.
- **`atime=off`, `compression=lz4`** (cheap, and bbolt pages compress well),
  **`logbias=latency`**.
- **ARC and the page cache both hold mmapped pages.** Cap the ARC
  (`zfs_arc_max` on Linux, `vfs.zfs.arc_max` on FreeBSD) so that it and the
  page cache together fit in memory.
- **`sync=standard` with a SLOG** for a database that is the only copy of its
  data; `sync=disabled` only for one that can be rebuilt
  ([DEPLOYMENT.md](DEPLOYMENT.md#spinning-disks-and-fsync)).

## Windows

- Keep the database on a local NTFS volume on SSD.
- Exclude the data directory from Defender real-time scanning.
- The same rules about shutdown apply: stop the service, do not kill it.

## GPU

mddb does not use a GPU. Vector search runs on the CPU with SIMD (AVX2 on
amd64, NEON and SME on arm64), and an HNSW query takes well under a
millisecond, so a GPU would add a transfer for no gain. Embeddings are where
a GPU helps: point mddb at an embedding server that runs on one, such as
Ollama with CUDA or Metal (`MDDB_EMBEDDING_PROVIDER=ollama`,
[config.md](config.md)).
