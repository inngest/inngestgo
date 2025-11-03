package checkpoint

import (
	"time"

	"github.com/inngest/inngestgo/internal/checkpoint"
)

type Config = checkpoint.Config

const (
	// AllSteps attempts to checkpoint as many steps as possible.
	AllSteps = 1_000
)

var (
	// CheckpointSafe is the safest configuration, which checkpoints after each step
	// in a blocking manner.
	//
	// By default, you should use this configuration.  You should also ALWAYS use this
	// configuration first, and only tune these parameters to further improve latency.
	ConfigSafe = &Config{}

	// ConfigPerformant is the least safe configuration, and runs as many steps as possible,
	// until a checkpoint is forced via an async step (eg. step.sleep, step.waitForEvent),
	// or the run ends.
	//
	// You should ONLY use this configuration if you care about performance over everything,
	// and are comfortable with steps potentially re-running.  Look at and use ConfigBlended,
	// or your own custom config, if you care about both performance and safety.
	//
	// It is NOT recommended to use this in serverless environments.
	ConfigPerformant = &Config{
		MaxSteps: AllSteps,
	}

	// ConfigBlended checkpoints after 3 steps or 3 seconds pass, giving a blend between
	// performance and safety.
	ConfigBlended = &Config{
		MaxSteps:    3,
		MaxInterval: 3 * time.Second,
	}
)
