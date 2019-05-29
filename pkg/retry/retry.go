package retry

import (
	"errors"
	"time"
)

var errMaxRetriesReached = errors.New("exceeded retry limit")

// Func represents functions that can be retried.
type Func func() error

// Do keeps trying the function until the second argument
// returns false, or no error is returned.
func Do(maxRetries int, interval time.Duration, fn Func) error {
	var err error
	attempt := 1
	for {
		err = fn()
		if err == nil {
			return nil
		}
		attempt++
		if attempt > maxRetries {
			return errMaxRetriesReached
		}
	}
}

// IsMaxRetries checks whether the error is due to hitting the
// maximum number of retries or not.
func IsMaxRetries(err error) bool {
	return err == errMaxRetriesReached
}
