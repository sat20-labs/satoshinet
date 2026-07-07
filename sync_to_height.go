package main

import "sync"

func newSyncToHeightCallback(target int32, interrupt <-chan struct{}) func(int32) {
	if target <= 0 {
		return nil
	}

	srvrLog.Infof("Sync-to-height target enabled: %d", target)

	var once sync.Once
	return func(height int32) {
		if !syncToHeightReached(height, target) {
			return
		}
		once.Do(func() {
			srvrLog.Infof("Sync-to-height target %d reached at height %d; requesting shutdown",
				target, height)
			requestSyncToHeightShutdown(interrupt)
		})
	}
}

func syncToHeightReached(height, target int32) bool {
	return target > 0 && height >= target
}

func requestSyncToHeightShutdown(interrupt <-chan struct{}) {
	select {
	case shutdownRequestChannel <- struct{}{}:
	case <-interrupt:
	}
}
