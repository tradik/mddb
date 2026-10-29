package main

import (
	"errors"
	"fmt"

	"mddb/internal/migrate"
)

// migrateCollectionFiles brings every open collection file to this build's
// on-disk schema (DOC-017), in parallel, as the files were opened. Each file
// carries its own schema version, so one that fails leaves the others
// migrated and is retried at the next open. A file the server creates later
// starts without a version and is stamped the next time it is opened here.
func migrateCollectionFiles(cf *collectionFiles) error {
	all := cf.all()
	results := make(chan error, len(all))
	for c, db := range all {
		go func() {
			if err := migrate.Run(db); err != nil {
				results <- fmt.Errorf("collection %q: %w", c, err)
				return
			}
			results <- nil
		}()
	}
	var errs []error
	for range all {
		if err := <-results; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
