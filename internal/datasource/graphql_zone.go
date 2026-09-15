package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var (
	ErrNoData    = errors.New("zone source: no data received yet")
	ErrStaleData = errors.New("zone source: data is stale")
)

const (
	defaultStaleTTL       = 5 * time.Minute
	defaultReconnectMin   = 1 * time.Second
	defaultReconnectMax   = 30 * time.Second
	defaultReconnectScale = 2
)

// GraphQLZoneConfig configures a GraphQL zone data source.
type GraphQLZoneConfig struct {
	GatewayURL string
	ZoneID     string
	StaleTTL   time.Duration
}

// GraphQLZoneSource subscribes to a gateway's zoneUpdate GraphQL subscription
// and caches the latest temperature and humidity for a specific zone.
type GraphQLZoneSource struct {
	config GraphQLZoneConfig

	mu         sync.RWMutex
	lastTemp   *float64
	lastHumid  *float64
	lastUpdate time.Time
}

// NewGraphQLZoneSource creates a new zone data source. Call Run() to start
// the subscription loop.
func NewGraphQLZoneSource(config GraphQLZoneConfig) *GraphQLZoneSource {
	if config.StaleTTL <= 0 {
		config.StaleTTL = defaultStaleTTL
	}
	return &GraphQLZoneSource{config: config}
}

// Temperature returns the latest cached temperature in °C.
func (s *GraphQLZoneSource) Temperature() (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.lastTemp == nil {
		return 0, ErrNoData
	}
	if time.Since(s.lastUpdate) > s.config.StaleTTL {
		return 0, ErrStaleData
	}
	return *s.lastTemp, nil
}

// Humidity returns the latest cached humidity in %.
func (s *GraphQLZoneSource) Humidity() (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.lastHumid == nil {
		return 0, ErrNoData
	}
	if time.Since(s.lastUpdate) > s.config.StaleTTL {
		return 0, ErrStaleData
	}
	return *s.lastHumid, nil
}

// Update sets the cached temperature and humidity. Exported for testing.
func (s *GraphQLZoneSource) Update(temp, humidity float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTemp = &temp
	s.lastHumid = &humidity
	s.lastUpdate = time.Now()
}

// Run starts the GraphQL subscription loop with automatic reconnect.
// Blocks until the context is canceled.
func (s *GraphQLZoneSource) Run(ctx context.Context) error {
	backoff := defaultReconnectMin

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := s.subscribeOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		log.Printf("graphql zone source disconnected: %v; reconnecting in %v", err, backoff)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}

		backoff = backoff * time.Duration(defaultReconnectScale)
		if backoff > defaultReconnectMax {
			backoff = defaultReconnectMax
		}
	}
}

func (s *GraphQLZoneSource) subscribeOnce(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, s.config.GatewayURL, &websocket.DialOptions{
		Subprotocols: []string{"graphql-ws"},
		HTTPHeader:   http.Header{},
	})
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer func() {
		_ = conn.CloseNow() // best-effort abort after the subscription loop exits
	}()

	// Send connection_init.
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "connection_init"}); err != nil {
		return fmt.Errorf("connection_init: %w", err)
	}

	// Read connection_ack.
	var ack map[string]interface{}
	if err := wsjson.Read(ctx, conn, &ack); err != nil {
		return fmt.Errorf("connection_ack: %w", err)
	}

	// Send subscription start.
	subMsg := map[string]interface{}{
		"id":   "1",
		"type": "start",
		"payload": map[string]interface{}{
			"query": `subscription { zoneUpdate { id currentTempC currentHumidityPct } }`,
		},
	}
	if err := wsjson.Write(ctx, conn, subMsg); err != nil {
		return fmt.Errorf("subscription start: %w", err)
	}

	// Read subscription events.
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var msg json.RawMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			if errors.Is(err, io.EOF) || websocket.CloseStatus(err) != -1 {
				return fmt.Errorf("websocket closed: %w", err)
			}
			return fmt.Errorf("read: %w", err)
		}

		s.processMessage(msg)
	}
}

type gqlMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
}

type gqlDataPayload struct {
	Data struct {
		ZoneUpdate struct {
			ID                 string   `json:"id"`
			CurrentTempC       *float64 `json:"currentTempC"`
			CurrentHumidityPct *float64 `json:"currentHumidityPct"`
		} `json:"zoneUpdate"`
	} `json:"data"`
}

func (s *GraphQLZoneSource) processMessage(raw json.RawMessage) {
	var msg gqlMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	if msg.Type != "data" {
		return
	}

	var payload gqlDataPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	zone := payload.Data.ZoneUpdate
	if zone.ID != s.config.ZoneID {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if zone.CurrentTempC != nil {
		s.lastTemp = zone.CurrentTempC
	}
	if zone.CurrentHumidityPct != nil {
		s.lastHumid = zone.CurrentHumidityPct
	}
	s.lastUpdate = time.Now()
}
