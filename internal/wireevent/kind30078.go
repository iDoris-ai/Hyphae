// Package wireevent classifies Hyphae's application events that share Nostr
// kind 30078. Keep this package independent from profile and messaging so
// their parsers can enforce the same wire discriminator without a cycle.
package wireevent

import (
	"encoding/hex"
	"fmt"
	"strings"

	"fiatjaf.com/nostr"
)

const (
	Kind30078 = 30078

	ProfileCategory = "profile"
	ProfileDTag     = "agent-profile"

	MessageCategory = "agent"
	MessageVersion  = "v1"
	MessageDPrefix  = "agent-message:"
)

// Class identifies the application schema carried by kind 30078.
type Class uint8

const (
	ClassUnknown Class = iota
	ClassProfile
	ClassMessage
)

// Classify30078 classifies an event's tags. The caller must first verify that
// the event kind is Kind30078, and must still validate the signature and any
// recipient-specific constraints before storing or acting on the event.
//
// Classification tags c, d, v, and p must be unique, two-element tags. The
// classifier rejects ambiguous combinations and unknown d namespaces rather
// than guessing from content.
func Classify30078(tags nostr.Tags) (Class, error) {
	c, hasC, err := uniqueTagValue(tags, "c")
	if err != nil {
		return ClassUnknown, err
	}
	d, hasD, err := uniqueTagValue(tags, "d")
	if err != nil {
		return ClassUnknown, err
	}
	v, hasV, err := uniqueTagValue(tags, "v")
	if err != nil {
		return ClassUnknown, err
	}
	p, hasP, err := uniqueTagValue(tags, "p")
	if err != nil {
		return ClassUnknown, err
	}
	if !hasC || c == "" {
		return ClassUnknown, fmt.Errorf("kind 30078 requires exactly one non-empty c tag")
	}

	switch c {
	case ProfileCategory:
		if !hasD || d != ProfileDTag {
			return ClassUnknown, fmt.Errorf("profile event requires d=%q", ProfileDTag)
		}
		if hasV || hasP {
			return ClassUnknown, fmt.Errorf("profile event conflicts with message discriminator tags")
		}
		return ClassProfile, nil

	case MessageCategory:
		if !hasV || v != MessageVersion {
			return ClassUnknown, fmt.Errorf("message event requires v=%q", MessageVersion)
		}
		if !hasP || !isPublicKeyHex(p) {
			return ClassUnknown, fmt.Errorf("message event requires exactly one 32-byte hex p tag")
		}
		if hasD && !isMessageDTag(d) {
			return ClassUnknown, fmt.Errorf("unknown or conflicting kind 30078 d namespace")
		}
		return ClassMessage, nil

	default:
		return ClassUnknown, fmt.Errorf("unknown kind 30078 c namespace %q", c)
	}
}

func uniqueTagValue(tags nostr.Tags, name string) (string, bool, error) {
	var value string
	found := false
	for _, tag := range tags {
		if len(tag) == 0 || tag[0] != name {
			continue
		}
		if found {
			return "", false, fmt.Errorf("duplicate kind 30078 %q discriminator tag", name)
		}
		if len(tag) != 2 || tag[1] == "" {
			return "", false, fmt.Errorf("malformed kind 30078 %q discriminator tag", name)
		}
		value = tag[1]
		found = true
	}
	return value, found, nil
}

func isPublicKeyHex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func isMessageDTag(value string) bool {
	if strings.HasPrefix(value, MessageDPrefix) {
		suffix := strings.TrimPrefix(value, MessageDPrefix)
		return isHexOfLength(suffix, 8) || isHexOfLength(suffix, 16) || isHexOfLength(suffix, 32)
	}
	// Legacy signed messages were published with no d tag, a 16-hex d, or a
	// 32-hex d (the daemon auto-reply path). Accept those only after c/v/p have
	// already uniquely classified the event as a message.
	return isHexOfLength(value, 8) || isHexOfLength(value, 16)
}

func isHexOfLength(value string, bytes int) bool {
	if len(value) != bytes*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
