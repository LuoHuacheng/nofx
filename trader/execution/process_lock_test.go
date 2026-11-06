package execution

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func processLockDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "t.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return db, dir
}

func TestProcessLockRejectsSecondOwner(t *testing.T) {
	ctx := context.Background()
	db, dir := processLockDB(t)

	release, err := AcquireProcessLock(ctx, db, dir)
	if err != nil {
		t.Fatalf("first owner refused: %v", err)
	}

	secondRelease, secondOwnerErr := AcquireProcessLock(ctx, db, dir)
	if secondOwnerErr == nil {
		t.Fatal("two execution owners acquired the same database")
	}
	if secondRelease != nil {
		secondRelease()
	}

	if err := release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	thirdRelease, err := AcquireProcessLock(ctx, db, dir)
	if err != nil {
		t.Fatalf("lock not reusable after release: %v", err)
	}
	if err := thirdRelease(); err != nil {
		t.Fatalf("third release failed: %v", err)
	}
}

func TestProcessLockReleaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, dir := processLockDB(t)

	release, err := AcquireProcessLock(ctx, db, dir)
	if err != nil {
		t.Fatalf("first owner refused: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("first release failed: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("second release failed: %v", err)
	}

	otherRelease, err := AcquireProcessLock(ctx, db, dir)
	if err != nil {
		t.Fatalf("lock not reusable after idempotent release: %v", err)
	}
	if err := otherRelease(); err != nil {
		t.Fatalf("release after reacquire failed: %v", err)
	}
}
