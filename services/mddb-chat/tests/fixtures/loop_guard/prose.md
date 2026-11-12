To back up MDDB without stopping it, call `POST /v1/backup` with the name of the file you want; the server writes a consistent snapshot of every collection while it keeps serving reads and writes. A backup taken this way can be restored with `POST /v1/restore`, which replaces the current data with the snapshot's.

The settings that matter:

- `MDDB_BACKUP_DIR` is where backups are written. The default is the data directory, which is fine for a quick test and wrong for production: a disk that dies takes the backups with it.
- `MDDB_BACKUP_RETENTION` is how many backups are kept. The default is seven; older ones are removed after each new backup.
- `MDDB_BACKUP_SCHEDULE` is a cron expression for scheduled backups. The default is empty, which means no schedule.
- `MDDB_BACKUP_COMPRESS` turns on gzip. The default is on; turn it off only if the disk is faster than the CPU.

A restore needs the server to be idle for a moment, because it swaps the data files underneath it. Plan it for a quiet hour, and take a fresh backup first, so a restore of the wrong file can itself be undone.

If you run MDDB in Kubernetes, mount the backup directory from a volume that outlives the pod, and copy the files off the cluster on a schedule of their own. A backup that lives next to the data it protects is a copy, not a backup.
