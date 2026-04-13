// Package types 提供共享的数据类型定义
package types

// Identity 表示用户身份（不包含私钥）
type Identity struct {
	Nickname string `json:"nickname"`
	Npub     string `json:"npub"`
	Nsec     string `json:"nsec"` // 注意：实际存储时需要加密
	Created  int64  `json:"created"`
}

// Contact 表示联系人
type Contact struct {
	Nickname string `json:"nickname"`
	Npub     string `json:"npub"`
	AddedAt  int64  `json:"added_at"`
}

// StoredMessage 表示存储的消息
type StoredMessage struct {
	ID            string `json:"id"`
	SenderNpub    string `json:"sender_npub"`
	RecipientNpub string `json:"recipient_npub"`
	Content       string `json:"content"`
	Plaintext     string `json:"plaintext,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	ReceivedAt    int64  `json:"received_at"`
	IsEncrypted   bool   `json:"is_encrypted"`
	IsIncoming    bool   `json:"is_incoming"`
	Relay         string `json:"relay"`
}

// OutboxEntry 表示待发送的消息
type OutboxEntry struct {
	ID            string   `json:"id"`
	EventJSON     string   `json:"event_json"`
	RecipientNpub string   `json:"recipient_npub"`
	Relays        []string `json:"relays"`
	RetryCount    int      `json:"retry_count"`
	MaxRetries    int      `json:"max_retries"`
	LastAttempt   int64    `json:"last_attempt"`
	CreatedAt     int64    `json:"created_at"`
	Status        string   `json:"status"` // "pending", "sent", "failed"
}

// KeyStore 存储所有身份和联系人
type KeyStore struct {
	DefaultIdentity string               `json:"default_identity"`
	Identities      map[string]*Identity `json:"identities"`
	Contacts        map[string]*Contact  `json:"contacts"`
}

// MessageStore 存储消息历史
type MessageStore struct {
	Messages []StoredMessage `json:"messages"`
}

// Outbox 存储待发送消息
type Outbox struct {
	Entries []OutboxEntry `json:"entries"`
}
