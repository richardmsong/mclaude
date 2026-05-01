package drivers

import (
	"context"
	"fmt"
)

// DroidDriver is a stub implementation of CLIDriver for the Factory Droid backend.
// Per ADR-0005 Phase 4, the full JSON-RPC 2.0 protocol implementation is planned
// but not yet implemented. Launch and Resume return an error indicating the driver
// is not yet available.
type DroidDriver struct{}

// NewDroidDriver creates a DroidDriver stub.
func NewDroidDriver() *DroidDriver {
	return &DroidDriver{}
}

func (d *DroidDriver) Backend() CLIBackend { return BackendDroid }

func (d *DroidDriver) DisplayName() string { return "Factory Droid" }

func (d *DroidDriver) Capabilities() CLICapabilities {
	return CLICapabilities{
		HasThinking:      true,
		HasSubagents:     false, // Droid uses missions (different model), not subagents
		HasSkills:        true,
		HasPlanMode:      true,  // Droid has spec mode
		HasMissions:      true,
		HasEventStream:   true,
		HasSessionResume: true,
		ThinkingLabel:    "Thinking",
		PermissionModes:  []string{"auto", "managed"},
	}
}

// Launch is not yet implemented for DroidDriver.
func (d *DroidDriver) Launch(ctx context.Context, opts LaunchOptions) (*Process, error) {
	return nil, fmt.Errorf("droid driver: not yet implemented")
}

// Resume is not yet implemented for DroidDriver.
func (d *DroidDriver) Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error) {
	return nil, fmt.Errorf("droid driver: not yet implemented")
}

// SendMessage is not yet implemented for DroidDriver.
func (d *DroidDriver) SendMessage(proc *Process, msg UserMessage) error {
	return fmt.Errorf("droid driver: not yet implemented")
}

// SendPermissionResponse is not yet implemented for DroidDriver.
func (d *DroidDriver) SendPermissionResponse(proc *Process, requestID string, allow bool) error {
	return fmt.Errorf("droid driver: not yet implemented")
}

// UpdateConfig is not yet implemented for DroidDriver.
func (d *DroidDriver) UpdateConfig(proc *Process, cfg SessionConfig) error {
	return fmt.Errorf("droid driver: not yet implemented")
}

// Interrupt is not yet implemented for DroidDriver.
func (d *DroidDriver) Interrupt(proc *Process) error {
	return fmt.Errorf("droid driver: not yet implemented")
}

// ReadEvents is not yet implemented for DroidDriver.
func (d *DroidDriver) ReadEvents(proc *Process, out chan<- DriverEvent) error {
	return fmt.Errorf("droid driver: not yet implemented")
}
