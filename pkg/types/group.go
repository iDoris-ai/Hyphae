// Package types provides group chat type definitions
package types

// Group represents a chat group
type Group struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Creator     string   `json:"creator"` // Creator npub
	Members     []string `json:"members"` // Member npubs
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

// GroupMember represents a group's member with metadata
type GroupMember struct {
	GroupID   string `json:"group_id"`
	Npub      string `json:"npub"`
	Nickname  string `json:"nickname,omitempty"`
	JoinedAt  int64  `json:"joined_at"`
	IsAdmin   bool   `json:"is_admin"`
}

// GroupMessage represents a message in a group
type GroupMessage struct {
	ID        string `json:"id"`
	EventID   string `json:"event_id"`
	GroupID   string `json:"group_id"`
	Sender    string `json:"sender"` // Sender npub
	Content   string `json:"content"`
	Plaintext string `json:"plaintext,omitempty"`
	CreatedAt int64  `json:"created_at"`
	IsEncrypted bool `json:"is_encrypted"`
}

// IsMember checks if an npub is a group member
func (g *Group) IsMember(npub string) bool {
	for _, m := range g.Members {
		if m == npub {
			return true
		}
	}
	return false
}

// AddMember adds a member to the group
func (g *Group) AddMember(npub string) {
	if !g.IsMember(npub) {
		g.Members = append(g.Members, npub)
	}
}

// RemoveMember removes a member from the group
func (g *Group) RemoveMember(npub string) {
	newMembers := make([]string, 0, len(g.Members))
	for _, m := range g.Members {
		if m != npub {
			newMembers = append(newMembers, m)
		}
	}
	g.Members = newMembers
}
