package main

import (
	"os"

	"github.com/sunxu/relay-station-control/internal/dingtalk"
	controljobs "github.com/sunxu/relay-station-control/internal/jobs"
)

// loadDingTalkConfig keeps environment parsing at the composition boundary.
// A missing URL is a valid disabled configuration; it must not remove the
// production job kind from the process catalog.
func loadDingTalkConfig() (dingtalk.Config, error) {
	return dingtalk.LoadConfig(os.Getenv)
}

// newProductionJobRegistry always registers the production kind.  Enabled is
// an executor delivery switch, not a catalog switch, so queued jobs retain a
// stable registry contract when the URL is absent.
func newProductionJobRegistry(config dingtalk.Config) (*controljobs.Registry, error) {
	return controljobs.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(config)))
}
