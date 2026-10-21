package messaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// ErrUnsupportedGroupVersion is returned when a reserved payload is not v1.
// The group state machine is not called.
var ErrUnsupportedGroupVersion = errors.New("unsupported group dispatch version")

// GroupMachines receives one verified reserved payload after the version gate.
type GroupMachines struct {
	Invite   func(VerifiedAgentMessage) error
	Accept   func(VerifiedAgentMessage) error
	Activate func(VerifiedAgentMessage) error
	Message  func(VerifiedAgentMessage) error
}

// DispatchVerifiedGroup hands a verified reserved payload to the invite,
// accept, activation, or message function. A non-v1 version returns first.
func DispatchVerifiedGroup(machines GroupMachines, message VerifiedAgentMessage) error {
	if !message.Valid() || message.Route() != AgentRouteReservedGroup {
		return errors.New("reserved group Agent message is required")
	}
	version, payload, ok := strings.Cut(strings.TrimPrefix(message.Plaintext(), reservedGroupPayloadPrefix), "\n")
	if !ok || version != "v1" {
		return ErrUnsupportedGroupVersion
	}
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	var head struct {
		Type string `json:"type"`
	}
	if err := decoder.Decode(&head); err != nil {
		return err
	}
	var call func(VerifiedAgentMessage) error
	switch head.Type {
	case "invite":
		call = machines.Invite
	case "accept":
		call = machines.Accept
	case "activate":
		call = machines.Activate
	case "message":
		call = machines.Message
	default:
		return errors.New("unsupported group dispatch type")
	}
	if call == nil {
		return errors.New("group dispatch handler is missing")
	}
	return call(message)
}
