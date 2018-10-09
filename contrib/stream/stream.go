package main

import (
	"github.com/dannyluong408/marketstore/contrib/stream/streamtrigger"
	"github.com/dannyluong408/marketstore/plugins/trigger"
)

// NewTrigger returns a new on-disk aggregate trigger based on the configuration.
func NewTrigger(conf map[string]interface{}) (trigger.Trigger, error) {
	return streamtrigger.NewTrigger(conf)
}

func main() {
}
