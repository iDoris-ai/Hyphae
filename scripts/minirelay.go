// Mini Nostr Relay for Testing
// Usage: go run scripts/minirelay.go
// Or: go build -o bin/minirelay scripts/minirelay.go && ./bin/minirelay

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Event struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig"`
}

type Filter struct {
	IDs     []string   `json:"ids,omitempty"`
	Authors []string   `json:"authors,omitempty"`
	Kinds   []int      `json:"kinds,omitempty"`
	Since   *int64     `json:"since,omitempty"`
	Until   *int64     `json:"until,omitempty"`
	Tags    [][]string `json:"#e,omitempty"`
	Limit   int        `json:"limit,omitempty"`
}

type Relay struct {
	events    []Event
	eventsMu  sync.RWMutex
	clients   map[*Client]bool
	clientsMu sync.Mutex
}

type Client struct {
	relay *Relay
	conn  *websocket.Conn
	subs  map[string]Filter
	send  chan []byte
}

func NewRelay() *Relay {
	return &Relay{
		events:  make([]Event, 0),
		clients: make(map[*Client]bool),
	}
}

func (r *Relay) addEvent(evt Event) {
	r.eventsMu.Lock()
	r.events = append(r.events, evt)
	r.eventsMu.Unlock()

	// Broadcast to subscribed clients
	r.clientsMu.Lock()
	for client := range r.clients {
		for subID, filter := range client.subs {
			if matchesFilter(evt, filter) {
				msg, _ := json.Marshal([]interface{}{"EVENT", subID, evt})
				select {
				case client.send <- msg:
				default:
				}
			}
		}
	}
	r.clientsMu.Unlock()
}

func (r *Relay) queryEvents(filter Filter) []Event {
	r.eventsMu.RLock()
	defer r.eventsMu.RUnlock()

	var results []Event
	for _, evt := range r.events {
		if matchesFilter(evt, filter) {
			results = append(results, evt)
			if filter.Limit > 0 && len(results) >= filter.Limit {
				break
			}
		}
	}
	return results
}

func matchesFilter(evt Event, filter Filter) bool {
	if len(filter.IDs) > 0 {
		found := false
		for _, id := range filter.IDs {
			if id == evt.ID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.Authors) > 0 {
		found := false
		for _, author := range filter.Authors {
			if author == evt.PubKey {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.Kinds) > 0 {
		found := false
		for _, kind := range filter.Kinds {
			if kind == evt.Kind {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if filter.Since != nil && evt.CreatedAt < *filter.Since {
		return false
	}

	if filter.Until != nil && evt.CreatedAt > *filter.Until {
		return false
	}

	return true
}

func (r *Relay) handleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	client := &Client{
		relay: r,
		conn:  conn,
		subs:  make(map[string]Filter),
		send:  make(chan []byte, 256),
	}

	r.clientsMu.Lock()
	r.clients[client] = true
	r.clientsMu.Unlock()

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.relay.clientsMu.Lock()
		delete(c.relay.clients, c)
		c.relay.clientsMu.Unlock()
		c.conn.Close()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			return
		}

		var msg []interface{}
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		if len(msg) == 0 {
			continue
		}

		switch msg[0].(string) {
		case "EVENT":
			if len(msg) >= 2 {
				var evt Event
				evtData, _ := json.Marshal(msg[1])
				json.Unmarshal(evtData, &evt)
				c.relay.addEvent(evt)
				okMsg, _ := json.Marshal([]interface{}{"OK", evt.ID, true, ""})
				c.send <- okMsg
			}

		case "REQ":
			if len(msg) >= 3 {
				subID := msg[1].(string)
				var filter Filter
				filterData, _ := json.Marshal(msg[2])
				json.Unmarshal(filterData, &filter)

				c.subs[subID] = filter

				events := c.relay.queryEvents(filter)
				for _, evt := range events {
					resp, _ := json.Marshal([]interface{}{"EVENT", subID, evt})
					c.send <- resp
				}

				eose, _ := json.Marshal([]interface{}{"EOSE", subID})
				c.send <- eose
			}

		case "CLOSE":
			if len(msg) >= 2 {
				subID := msg[1].(string)
				delete(c.subs, subID)
			}
		}
	}
}

func (c *Client) writePump() {
	for msg := range c.send {
		c.conn.WriteMessage(websocket.TextMessage, msg)
	}
}

func (r *Relay) handleNIP11(w http.ResponseWriter, req *http.Request) {
	if strings.Contains(req.Header.Get("Accept"), "application/nostr+json") {
		w.Header().Set("Content-Type", "application/nostr+json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":          "Agent-Speaker Mini Relay",
			"description":   "Lightweight development relay",
			"pubkey":        "",
			"contact":       "",
			"supported_nips": []int{1, 2, 4, 11, 20},
			"software":      "minirelay",
			"version":       "0.1.0",
		})
		return
	}
	w.Write([]byte("Agent-Speaker Mini Relay\n"))
}

func main() {
	relay := NewRelay()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		relay.eventsMu.RLock()
		count := len(relay.events)
		relay.eventsMu.RUnlock()
		fmt.Fprintf(w, "events_total %d\n", count)
	})

	// Main endpoint (WebSocket + HTTP)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			relay.handleWebSocket(w, r)
		} else {
			relay.handleNIP11(w, r)
		}
	})

	port := "7777"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	log.Printf("🚀 Mini Relay starting on ws://localhost:%s", port)
	log.Printf("   HTTP: http://localhost:%s", port)
	log.Printf("   WebSocket: ws://localhost:%s", port)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: nil,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
