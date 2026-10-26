package groupchat

import (
	"context"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

type groupListEntry struct {
	GroupID   string   `json:"group_id"`
	Name      string   `json:"name"`
	State     string   `json:"state"`
	Creator   string   `json:"creator_npub"`
	Members   []string `json:"members"`
	UpdatedAt int64    `json:"updated_at"`
}

type memberEntry struct {
	Npub      string `json:"npub"`
	State     string `json:"state"`
	IsCreator bool   `json:"is_creator"`
}

var listCmd = &cli.Command{
	Name:  "list",
	Usage: "List the groups this identity belongs to",
	Flags: localIdentityFlags(),
	Action: func(ctx context.Context, c *cli.Command) error {
		session, err := openSession(c)
		if err != nil {
			return err
		}
		defer session.db.Close()
		groups, err := session.store.ListGroups(session.me)
		if err != nil {
			return err
		}
		entries := make([]groupListEntry, 0, len(groups))
		for _, g := range groups {
			entries = append(entries, groupListEntry{GroupID: g.ID, Name: g.Name, State: string(g.State),
				Creator: g.Creator, Members: append([]string{}, g.Roster...), UpdatedAt: g.UpdatedAt})
		}
		common.Emit(common.JSONMode(c), map[string]any{"groups": entries}, func() {
			for _, e := range entries {
				fmt.Printf("%s %s %s\n", e.GroupID, e.State, e.Name)
			}
		})
		return nil
	},
}

var membersCmd = &cli.Command{
	Name:  "members",
	Usage: "List one group's members and their state",
	Flags: append([]cli.Flag{
		&cli.StringFlag{Name: "group-id", Required: true, Usage: "Group id"},
	}, localIdentityFlags()...),
	Action: func(ctx context.Context, c *cli.Command) error {
		session, err := openSession(c)
		if err != nil {
			return err
		}
		defer session.db.Close()
		members, err := session.store.listMembers(session.me, c.String("group-id"))
		if err != nil {
			return err
		}
		common.Emit(common.JSONMode(c), map[string]any{"members": members}, func() {
			for _, m := range members {
				fmt.Println(m.Npub, m.State)
			}
		})
		return nil
	},
}

func (s *Store) listMembers(localNpub, groupID string) ([]memberEntry, error) {
	if _, err := s.GetGroup(localNpub, groupID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT npub, state, is_creator FROM groupchat_members WHERE local_npub = ? AND group_id = ? ORDER BY is_creator DESC, npub`, localNpub, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []memberEntry{}
	for rows.Next() {
		var m memberEntry
		var creator int
		if err := rows.Scan(&m.Npub, &m.State, &creator); err != nil {
			return nil, err
		}
		m.IsCreator = creator == 1
		members = append(members, m)
	}
	return members, rows.Err()
}
