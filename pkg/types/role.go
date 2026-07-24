package types

// Role marks whether a contact or group member is a human or an Agent. This
// is a label only, not a permission bitmask or RBAC system — enforcement is
// out of scope for now (see specs/m1.5/tasks/05-member-role-model.md), this
// just gives later milestones (owner-attestation, autonomous Agent behavior)
// something to key off of.
type Role string

const (
	RoleHuman Role = "human"
	RoleAgent Role = "agent"
)

// String satisfies fmt.Stringer so Role prints as its bare value in
// Printf("%s", ...) / table output.
func (r Role) String() string {
	if r == "" {
		return string(RoleHuman)
	}
	return string(r)
}

// IsValid reports whether r is an explicitly recognized role. The zero value
// ("") is deliberately NOT valid here -- that's the legacy/deserialization
// default handled by String(), not something callers should be able to pass
// in on purpose. Use this to validate role values at write boundaries
// (contact/group-member creation), independent of any single CLI command's
// own flag validation.
func (r Role) IsValid() bool {
	return r == RoleHuman || r == RoleAgent
}
