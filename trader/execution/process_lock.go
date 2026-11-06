package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"gorm.io/gorm"
)

const executionLockFile = "nofx-execution.lock"

// executionLockKey is the Postgres advisory lock id ("NOFX" in ASCII).
const executionLockKey = int64(0x4e4f4658)

var errExecutionOwned = errors.New("another process owns unattended execution on this database")

// AcquireProcessLock guarantees one execution owner per database. SQLite uses
// an OS advisory file lock next to the database; Postgres holds a session
// advisory lock on a dedicated connection. Losing the lock must freeze new
// entries, so the returned release is idempotent and the caller treats any
// later failure as fatal for new submissions.
func AcquireProcessLock(ctx context.Context, db *gorm.DB, dataDir string) (func() error, error) {
	if db.Dialector.Name() == "sqlite" {
		return acquireFileLock(filepath.Join(dataDir, executionLockFile))
	}
	return acquireSessionLock(ctx, db)
}

func acquireFileLock(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errExecutionOwned
		}
		return nil, err
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
				releaseErr = err
			}
			if err := f.Close(); err != nil && releaseErr == nil {
				releaseErr = err
			}
		})
		return releaseErr
	}, nil
}

func acquireSessionLock(ctx context.Context, db *gorm.DB) (func() error, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", executionLockKey).Scan(&ok); err != nil {
		conn.Close()
		return nil, err
	}
	if !ok {
		conn.Close()
		return nil, errExecutionOwned
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			var unlocked bool
			if err := conn.QueryRowContext(context.Background(), "SELECT pg_advisory_unlock($1)", executionLockKey).Scan(&unlocked); err != nil {
				releaseErr = err
			}
			if err := conn.Close(); err != nil && releaseErr == nil {
				releaseErr = err
			}
		})
		return releaseErr
	}, nil
}
