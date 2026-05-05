package drivers

import (
	"context"
	"fmt"
)

// GenericTerminalDriver is a stub implementation of CLIDriver for the generic
// terminal fallback backend. Per ADR-0005 Phase 7, the full PTY-based
// implementation (using creack/pty) is planned but not yet implemented.
// Launch and Resume return an error indicating the driver is not yet available.
//
// When implemented, this driver will spawn the CLI binary in a PTY and publish
// raw PTY bytes as text_delta events. HasEventStream is false — the SPA shows
// the Terminal tab only, not the conversation view.
type GenericTerminalDriver struct{}

// NewGenericTerminalDriver creates a GenericTerminalDriver stub.
func NewGenericTerminalDriver() *GenericTerminalDriver {
	return &GenericTerminalDriver{}
}

func (d *GenericTerminalDriver) Backend() CLIBackend { return BackendGenericTerminal }

func (d *GenericTerminalDriver) DisplayName() string { return "Terminal" }

func (d *GenericTerminalDriver) Capabilities() CLICapabilities {
	return CLICapabilities{
		HasThinking:      false,
		HasSubagents:     false,
		HasSkills:        false,
		HasPlanMode:      false,
		HasMissions:      false,
		HasEventStream:   false, // SPA shows Terminal tab only; no conversation view
		HasSessionResume: false,
		ThinkingLabel:    "",
	}
}

// Launch is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) Launch(ctx context.Context, opts LaunchOptions) (*Process, error) {
	return nil, fmt.Errorf("generic_terminal driver: not yet implemented")
}

// Resume is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error) {
	return nil, fmt.Errorf("generic_terminal driver: not yet implemented")
}

// SendMessage is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) SendMessage(proc *Process, msg UserMessage) error {
	return fmt.Errorf("generic_terminal driver: not yet implemented")
}

// SendPermissionResponse is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) SendPermissionResponse(proc *Process, requestID string, allow bool) error {
	return fmt.Errorf("generic_terminal driver: not yet implemented")
}

// UpdateConfig is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) UpdateConfig(proc *Process, cfg SessionConfig) error {
	return fmt.Errorf("generic_terminal driver: not yet implemented")
}

// Interrupt is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) Interrupt(proc *Process) error {
	return fmt.Errorf("generic_terminal driver: not yet implemented")
}

// ReadEvents is not yet implemented for GenericTerminalDriver.
func (d *GenericTerminalDriver) ReadEvents(proc *Process, out chan<- DriverEvent) error {
	return fmt.Errorf("generic_terminal driver: not yet implemented")
}
