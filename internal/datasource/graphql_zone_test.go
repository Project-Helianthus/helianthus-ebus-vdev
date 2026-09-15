package datasource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestGraphQLZoneSource_NoData(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	_, err := src.Temperature()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Temperature() err = %v; want ErrNoData", err)
	}

	_, err = src.Humidity()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Humidity() err = %v; want ErrNoData", err)
	}
}

func TestGraphQLZoneSource_Update(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	src.Update(21.5, 45.0)

	temp, err := src.Temperature()
	if err != nil {
		t.Fatalf("Temperature() err = %v", err)
	}
	if temp != 21.5 {
		t.Fatalf("Temperature() = %f; want 21.5", temp)
	}

	humid, err := src.Humidity()
	if err != nil {
		t.Fatalf("Humidity() err = %v", err)
	}
	if humid != 45.0 {
		t.Fatalf("Humidity() = %f; want 45.0", humid)
	}
}

func TestGraphQLZoneSource_StaleData(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
		StaleTTL:   1 * time.Millisecond,
	})

	src.Update(21.5, 45.0)
	time.Sleep(5 * time.Millisecond)

	_, err := src.Temperature()
	if !errors.Is(err, ErrStaleData) {
		t.Fatalf("Temperature() err = %v; want ErrStaleData", err)
	}

	_, err = src.Humidity()
	if !errors.Is(err, ErrStaleData) {
		t.Fatalf("Humidity() err = %v; want ErrStaleData", err)
	}
}

func TestGraphQLZoneSource_DefaultStaleTTL(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	if src.config.StaleTTL != defaultStaleTTL {
		t.Fatalf("StaleTTL = %v; want %v", src.config.StaleTTL, defaultStaleTTL)
	}
}

func TestGraphQLZoneSource_SubscribeOnceWithMaintainedWebsocket(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{"graphql-ws"},
		})
		if err != nil {
			t.Errorf("websocket Accept() err = %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		var init map[string]any
		if err := wsjson.Read(r.Context(), conn, &init); err != nil {
			t.Errorf("read connection_init: %v", err)
			return
		}
		if init["type"] != "connection_init" {
			t.Errorf("initial message type = %#v; want connection_init", init["type"])
			return
		}
		if err := wsjson.Write(r.Context(), conn, map[string]string{"type": "connection_ack"}); err != nil {
			t.Errorf("write connection_ack: %v", err)
			return
		}

		var start map[string]any
		if err := wsjson.Read(r.Context(), conn, &start); err != nil {
			t.Errorf("read subscription start: %v", err)
			return
		}
		if start["type"] != "start" {
			t.Errorf("subscription message type = %#v; want start", start["type"])
			return
		}

		event := map[string]any{
			"type": "data",
			"id":   "1",
			"payload": map[string]any{"data": map[string]any{"zoneUpdate": map[string]any{
				"id":                 "zone-2",
				"currentTempC":       22.75,
				"currentHumidityPct": 51.5,
			}}},
		}
		if err := wsjson.Write(r.Context(), conn, event); err != nil {
			t.Errorf("write subscription event: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "fixture complete")
	}))
	defer server.Close()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		ZoneID:     "zone-2",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := src.subscribeOnce(ctx); err == nil || !strings.Contains(err.Error(), "websocket closed") {
		t.Fatalf("subscribeOnce() err = %v; want normal websocket closure", err)
	}

	temp, err := src.Temperature()
	if err != nil || temp != 22.75 {
		t.Fatalf("Temperature() = %v, %v; want 22.75, nil", temp, err)
	}
	humidity, err := src.Humidity()
	if err != nil || humidity != 51.5 {
		t.Fatalf("Humidity() = %v, %v; want 51.5, nil", humidity, err)
	}
}

func TestGraphQLZoneSource_ProcessMessage(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	msg := []byte(`{"type":"data","id":"1","payload":{"data":{"zoneUpdate":{"id":"zone-2","currentTempC":22.5,"currentHumidityPct":55.0}}}}`)
	src.processMessage(msg)

	temp, err := src.Temperature()
	if err != nil {
		t.Fatalf("Temperature() err = %v", err)
	}
	if temp != 22.5 {
		t.Fatalf("Temperature() = %f; want 22.5", temp)
	}

	humid, err := src.Humidity()
	if err != nil {
		t.Fatalf("Humidity() err = %v", err)
	}
	if humid != 55.0 {
		t.Fatalf("Humidity() = %f; want 55.0", humid)
	}
}

func TestGraphQLZoneSource_ProcessMessage_WrongZone(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	msg := []byte(`{"type":"data","id":"1","payload":{"data":{"zoneUpdate":{"id":"zone-1","currentTempC":22.5,"currentHumidityPct":55.0}}}}`)
	src.processMessage(msg)

	_, err := src.Temperature()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Temperature() err = %v; want ErrNoData (wrong zone filtered)", err)
	}
}

func TestGraphQLZoneSource_ProcessMessage_NonDataType(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	msg := []byte(`{"type":"connection_ack"}`)
	src.processMessage(msg)

	_, err := src.Temperature()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Temperature() err = %v; want ErrNoData", err)
	}
}

func TestGraphQLZoneSource_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			src.Update(float64(i), float64(i))
		}(i)
		go func() {
			defer wg.Done()
			// Values are deliberately discarded; the race detector is the assertion.
			_, _ = src.Temperature()
			_, _ = src.Humidity()
		}()
	}
	wg.Wait()

	// Just verify no panic/race.
	_, err := src.Temperature()
	if err != nil {
		t.Fatalf("Temperature() err = %v after concurrent access", err)
	}
}
