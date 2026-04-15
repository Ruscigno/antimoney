// Package scheduler contains background workers for the antimoney backend.
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/services"
)

const (
	tickInterval       = 60 * time.Second
	activeSnapInterval = 5 * time.Minute
)

// StartSnapshotScheduler runs a background goroutine that:
//  1. Purges expired snapshots on every tick.
//  2. For books with active_mode=true, takes a snapshot every 5 minutes while
//     the book has been modified in the last 5 minutes (user is actively editing).
//  3. For books with frequency_hours>0, takes a scheduled snapshot once the
//     configured interval has elapsed since the last snapshot.
//
// The goroutine exits when ctx is cancelled (on server shutdown).
func StartSnapshotScheduler(ctx context.Context, svc *services.SnapshotService) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runTick(ctx, svc)
		}
	}
}

func runTick(ctx context.Context, svc *services.SnapshotService) {
	now := time.Now().UTC()

	// Always purge expired snapshots first.
	if err := svc.PurgeExpiredSnapshots(ctx); err != nil {
		log.Printf("snapshot scheduler: purge error: %v", err)
	}

	configs, err := svc.GetAllActiveConfigs(ctx)
	if err != nil {
		log.Printf("snapshot scheduler: get configs error: %v", err)
		return
	}

	for _, cfg := range configs {
		processConfig(ctx, svc, cfg, now)
	}
}

func processConfig(ctx context.Context, svc *services.SnapshotService, cfg models.SnapshotConfig, now time.Time) {
	// Active-mode check: fires every 5 min while data was changed within the last 5 min.
	if cfg.ActiveMode {
		lastChange, err := svc.GetLastDataChangeTime(ctx, cfg.BookGUID)
		if err != nil {
			log.Printf("snapshot scheduler [book=%s]: get last data change error: %v", cfg.BookGUID, err)
		} else if lastChange != nil && now.Sub(*lastChange) < activeSnapInterval {
			// User is actively editing — check whether we need a new active snapshot.
			lastSnap, err := svc.GetLastSnapshotTime(ctx, cfg.BookGUID)
			if err != nil {
				log.Printf("snapshot scheduler [book=%s]: get last snapshot time error: %v", cfg.BookGUID, err)
			} else if lastSnap == nil || now.Sub(*lastSnap) >= activeSnapInterval {
				if err := svc.TakeSnapshotForBook(ctx, cfg.BookGUID, "", models.SnapshotTriggerActive); err != nil {
					log.Printf("snapshot scheduler [book=%s]: active snapshot error: %v", cfg.BookGUID, err)
				} else {
					log.Printf("snapshot scheduler [book=%s]: active snapshot taken", cfg.BookGUID)
				}
				// Skip frequency check this tick — one snapshot per tick is enough.
				return
			}
		}
	}

	// Frequency-based check.
	if cfg.FrequencyHours > 0 {
		threshold := time.Duration(cfg.FrequencyHours) * time.Hour
		lastSnap, err := svc.GetLastSnapshotTime(ctx, cfg.BookGUID)
		if err != nil {
			log.Printf("snapshot scheduler [book=%s]: get last snapshot time error: %v", cfg.BookGUID, err)
			return
		}
		if lastSnap == nil || now.Sub(*lastSnap) >= threshold {
			if err := svc.TakeSnapshotForBook(ctx, cfg.BookGUID, "", models.SnapshotTriggerScheduled); err != nil {
				log.Printf("snapshot scheduler [book=%s]: scheduled snapshot error: %v", cfg.BookGUID, err)
			} else {
				log.Printf("snapshot scheduler [book=%s]: scheduled snapshot taken", cfg.BookGUID)
			}
		}
	}
}
