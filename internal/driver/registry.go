package driver

import (
	"fmt"
	"sync"
)

var (
	mu      sync.RWMutex
	drivers = make(map[string]Driver)
)

func Register(name string, d Driver) {
	mu.Lock()
	defer mu.Unlock()
	drivers[name] = d
}

func Lookup(name string) (Driver, error) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("unknown CLAW_TYPE %q: no registered driver", name)
	}
	return d, nil
}
