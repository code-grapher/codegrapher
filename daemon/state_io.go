package daemon

import (
	"os"
	"time"
)

const (
	stateIOAttempts = 50
	stateIORetry    = 5 * time.Millisecond
)

func readStateFile(path string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, err := os.ReadFile(path)
		if err == nil || attempt+1 == stateIOAttempts || !isTransientStateIOError(err) {
			return data, err
		}
		time.Sleep(stateIORetry)
	}
}

func commitStateFile(source, destination string) error {
	for attempt := 0; ; attempt++ {
		err := os.Rename(source, destination)
		if err == nil || attempt+1 == stateIOAttempts || !isTransientStateIOError(err) {
			return err
		}
		time.Sleep(stateIORetry)
	}
}
