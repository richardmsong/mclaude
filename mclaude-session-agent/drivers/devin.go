package drivers

import (
	"context"
	"fmt"
)

// DevinACPDriver is a stub implementation of CLIDriver for the Devin ACP backend.
// Per ADR-0005 Phase 5, the full ACP (JSON-RPC 2.0 over stdio) implementation is
// planned but not yet implemented. Launch and Resume return an error indicating
// the driver is not yet available.
type DevinACPDriver struct{}

// NewDevinACPDriver creates a DevinACPDriver stub.
func NewDevinACPDriver() *DevinACPDriver {
	return &DevinACPDriver{}
}

func (d *DevinACPDriver) Backend() CLIBackend { return BackendDevinACP }

func (d *DevinACPDriver) DisplayName() string { return "Devin ACP" }

func (d *DevinACPDriver) Capabilities() CLICapabilities {
	return CLICapabilities{
		HasThinking:      true, // role=thought in ACP
		HasSubagents:     false,
		HasSkills:        true, // ACP slash commands
		HasPlanMode:      true, // /plan mode
		HasMissions:      false,
		HasEventStream:   true,
		HasSessionResume: true, // session/load
		ThinkingLabel:    "Thinking",
		PermissionModes:  []string{"auto", "managed"},
	}
}

// Launch is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) Launch(ctx context.Context, opts LaunchOptions) (*Process, error) {
	return nil, fmt.Errorf("devin_acp driver: not yet implemented")
}

// Resume is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error) {
	return nil, fmt.Errorf("devin_acp driver: not yet implemented")
}

// SendMessage is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) SendMessage(proc *Process, msg UserMessage) error {
	return fmt.Errorf("devin_acp driver: not yet implemented")
}

// SendPermissionResponse is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) SendPermissionResponse(proc *Process, requestID string, allow bool) error {
	return fmt.Errorf("devin_acp driver: not yet implemented")
}

// UpdateConfig is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) UpdateConfig(proc *Process, cfg SessionConfig) error {
	return fmt.Errorf("devin_acp driver: not yet implemented")
}

// Interrupt is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) Interrupt(proc *Process) error {
	return fmt.Errorf("devin_acp driver: not yet implemented")
}

// ReadEvents is not yet implemented for DevinACPDriver.
func (d *DevinACPDriver) ReadEvents(proc *Process, out chan<- DriverEvent) error {
	return fmt.Errorf("devin_acp driver: not yet implemented")
}
